package pgs

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
)

// rawGet requests a path as written, since http.Client cleans some.
func rawGet(t *testing.T, srv *httptest.Server, host, rawPath string) (int, string) {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", rawPath, host); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(body)
}

// A site and a tunnel to it only serve their own project, though an account's
// projects share a bucket.
func TestRequestsStayInTheProject(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	backends := map[string]func() (storage.StorageServe, error){
		"fs":     func() (storage.StorageServe, error) { return storage.NewStorageFS(logger, t.TempDir()) },
		"memory": func() (storage.StorageServe, error) { return storage.NewStorageMemory(map[string]map[string]string{}) },
	}
	for name, newStorage := range backends {
		t.Run(name, func(t *testing.T) {
			dbpool := NewPgsDb(logger)
			user := dbpool.Users[0]
			st, err := newStorage()
			if err != nil {
				t.Fatal(err)
			}
			bucket, err := st.UpsertBucket(shared.GetAssetBucketName(user.ID))
			if err != nil {
				t.Fatal(err)
			}
			for file, text := range map[string]string{
				"test/index.html":            "public",
				"test/a..b/page.html":        "dotted",
				"test-private/secret.txt":    "SECRET",
				"private-notes/index.html":   "SECRET",
				"private-notes/secret.txt":   "SECRET",
				"private-notes/dir/file.txt": "SECRET",
			} {
				if _, _, err := st.PutObject(bucket, file, strings.NewReader(text), &storage.ObjectInfo{}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := dbpool.InsertProject(user.ID, "private-notes", "private-notes"); err != nil {
				t.Fatal(err)
			}
			if err := dbpool.UpdateProjectAcl(user.ID, "private-notes", db.ProjectAcl{Type: "private"}); err != nil {
				t.Fatal(err)
			}

			cfg := NewConfig(logger, dbpool, st, discardPubsub{})
			cfg.Domain = "pgs.test"
			web := httptest.NewServer(newWebRouter(cfg))
			defer web.Close()
			routes := *newWebRouter(cfg)
			tunnelRouter := TunnelWebRouter{&routes, user.Name + "-test"}
			tunnelRouter.InitRouter()
			tunnel := httptest.NewServer(&tunnelRouter)
			defer tunnel.Close()

			host := user.Name + "-test.pgs.test"
			for _, srv := range []*httptest.Server{web, tunnel} {
				for rawPath, want := range map[string]string{"/": "public", "/a..b/page": "dotted", "/a..b/page.html": "dotted"} {
					if status, body := rawGet(t, srv, host, rawPath); status != http.StatusOK || body != want {
						t.Errorf("%s = %d %q, want %q", rawPath, status, body, want)
					}
				}
				for _, rawPath := range []string{
					"/%2e%2e/private-notes/secret.txt",
					"/..%2fprivate-notes/secret.txt",
					"/%2E%2E%2Fprivate-notes%2Fsecret.txt",
					"/x%2f..%2f..%2fprivate-notes/secret.txt",
					"/.%2e/private-notes/secret.txt",
					"/%2e%2e/private-notes/",
					"/%2e%2e/private-notes",
					"/%2e%2e/private-notes/dir/",
					"/%2e%2e/test-private/secret.txt",
					"/%2e%2e/",
					"/%2e%2e",
					"/%252e%252e/private-notes/secret.txt",
					"/%2e%2e/private-notes/x.png/s:500",
				} {
					status, body := rawGet(t, srv, host, rawPath)
					if strings.Contains(body, "SECRET") || strings.Contains(body, "secret") || status == http.StatusOK {
						t.Errorf("%s = %d %q", rawPath, status, body)
					}
				}
			}
		})
	}
}

func TestInProject(t *testing.T) {
	h := &ApiAssetHandler{ProjectDir: "blog"}
	for object, want := range map[string]bool{
		"blog/index.html":    true,
		"/blog/a/b.html":     true,
		"blog/x/../y":        true,
		"blog":               false,
		"blog2/index.html":   false,
		"blog/../shop/x":     false,
		"shop/x":             false,
		"":                   false,
		"../blog/index.html": true,
	} {
		if got := h.inProject(object); got != want {
			t.Errorf("inProject(%q) = %v, want %v", object, got, want)
		}
	}
}

// Names that aren't valid URLs are served, on the site and through a tunnel.
func TestServesLiteralNames(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	dbpool := NewPgsDb(logger)
	user := dbpool.Users[0]
	st, err := storage.NewStorageFS(logger, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := st.UpsertBucket(shared.GetAssetBucketName(user.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"100%.png", "100%.html", "50%/index.html", "a b.html", "a%20b.html", "x:y.html", "q?z.html", "h#z.html", "v1..2.html"} {
		if _, _, err := st.PutObject(bucket, "test/"+name, strings.NewReader(name), &storage.ObjectInfo{}); err != nil {
			t.Fatal(err)
		}
	}

	imgproxy := ""
	proxy := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		source, _ := base64.StdEncoding.DecodeString(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		imgproxy = string(source)
	}))
	defer proxy.Close()
	t.Setenv("IMGPROXY_URL", proxy.URL)

	cfg := NewConfig(logger, dbpool, st, discardPubsub{})
	cfg.Domain = "pgs.test"
	web := httptest.NewServer(newWebRouter(cfg))
	defer web.Close()
	routes := *newWebRouter(cfg)
	tunnelRouter := TunnelWebRouter{&routes, user.Name + "-test"}
	tunnelRouter.InitRouter()
	tunnel := httptest.NewServer(&tunnelRouter)
	defer tunnel.Close()

	for _, srv := range []*httptest.Server{web, tunnel} {
		for rawPath, want := range map[string]string{
			"/100%25.png":     "100%.png",
			"/100%25.html":    "100%.html",
			"/100%25":         "100%.html",
			"/50%25/":         "50%/index.html",
			"/a%20b.html":     "a b.html",
			"/a%2520b.html":   "a%20b.html",
			"/x:y.html":       "x:y.html",
			"/q%3Fz.html":     "q?z.html",
			"/h%23z.html":     "h#z.html",
			"/v1..2.html":     "v1..2.html",
			"/v1..2":          "v1..2.html",
			"/100%25.png?v=1": "100%.png",
		} {
			if status, body := rawGet(t, srv, user.Name+"-test.pgs.test", rawPath); status != http.StatusOK || body != want {
				t.Errorf("%s = %d %q, want %q", rawPath, status, body, want)
			}
		}

		for rawPath, want := range map[string]string{
			"/100%25.png/s:500":       "/" + bucket.Name + "/test/100%.png",
			"/a%252fb.png/s:500":      "/" + bucket.Name + "/test/a%2fb.png",
			"/%252e%252e/x.png/s:500": "/" + bucket.Name + "/test/%2e%2e/x.png",
		} {
			imgproxy = ""
			status, body := rawGet(t, srv, user.Name+"-test.pgs.test", rawPath)
			source, err := url.Parse(imgproxy)
			if err != nil || source.Path != want {
				t.Errorf("%s = %d %q, asked imgproxy for %q, %v, want %s", rawPath, status, body, imgproxy, err, want)
			}
		}
	}
}
