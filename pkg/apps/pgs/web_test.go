package pgs

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/mime"
	"github.com/picosh/pico/pkg/storage"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// var imgproxyContainer testcontainers.Container.
var imgproxyURL string

// setupContainerRuntime checks for a container runtime (podman/docker) and
// sets DOCKER_HOST so testcontainers can connect.
func setupContainerRuntime() bool {
	if cmd := exec.Command("podman", "info"); cmd.Run() == nil {
		_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
		xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
		if xdgRuntime != "" {
			socketPath := xdgRuntime + "/podman/podman.sock"
			if _, err := os.Stat(socketPath); err == nil {
				_ = os.Setenv("DOCKER_HOST", "unix://"+socketPath)
				return true
			}
		}
		return false
	}

	if cmd := exec.Command("docker", "info"); cmd.Run() == nil {
		return true
	}
	return false
}

func TestMain(m *testing.M) {
	ctx := context.Background()

	if !setupContainerRuntime() {
		fmt.Fprintf(os.Stderr, "Container runtime not available, skipping image manipulation tests\n")
		fmt.Fprintf(os.Stderr, "To run tests, either:\n")
		fmt.Fprintf(os.Stderr, "  - Start podman socket: systemctl --user start podman.socket\n")
		fmt.Fprintf(os.Stderr, "  - Start docker daemon\n")
		os.Exit(m.Run())
	}

	imgproxyContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "docker.io/darthsim/imgproxy:latest",
			ExposedPorts: []string{"8080/tcp"},
			WaitingFor:   wait.ForLog("INFO imgproxy is ready to listen"),
		},
		Started: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start imgproxy container (Docker/Podman may not be running): %s\n", err)
		fmt.Fprintf(os.Stderr, "Skipping image manipulation tests.\n")
		os.Exit(m.Run())
	}

	host, err := imgproxyContainer.Host(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get imgproxy host: %s\n", err)
		os.Exit(m.Run())
	}

	port, err := imgproxyContainer.MappedPort(ctx, "8080")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get imgproxy port: %s\n", err)
		os.Exit(m.Run())
	}

	imgproxyURL = fmt.Sprintf("http://%s:%s", host, port)
	_ = os.Setenv("IMGPROXY_URL", imgproxyURL)

	code := m.Run()

	_ = imgproxyContainer.Terminate(ctx)
	os.Exit(code)
}

// testStorage wraps storage.StorageServe to inject ObjectInfo fields that
// production backends (S3, GCS) provide but the in-memory test storage does not.
type testStorage struct {
	storage.StorageServe
}

func newTestStorage(st storage.StorageServe) *testStorage {
	return &testStorage{st}
}

func (t *testStorage) GetObject(bucket storage.Bucket, fpath string) (utils.ReadAndReaderAtCloser, *storage.ObjectInfo, error) {
	r, info, err := t.StorageServe.GetObject(bucket, fpath)
	if info.Metadata == nil {
		info.Metadata = make(http.Header)
	}
	info.Metadata.Set("content-type", mime.GetMimeType(fpath))
	info.LastModified = time.Now().UTC()
	info.ETag = "static-etag-for-testing-purposes"
	return r, info, err
}

type ApiExample struct {
	name        string
	path        string
	reqHeaders  map[string]string
	want        string
	wantUrl     string
	status      int
	contentType string

	storage map[string]map[string]string
}

type PgsDb struct {
	*pgsdb.MemoryDB
}

func NewPgsDb(logger *slog.Logger) *PgsDb {
	sb := pgsdb.NewDBMemory(logger)
	sb.SetupTestData()
	_, err := sb.InsertProject(sb.Users[0].ID, "test", "test")
	if err != nil {
		panic(err)
	}
	return &PgsDb{
		MemoryDB: sb,
	}
}

func (p *PgsDb) mkpath(path string) string {
	return fmt.Sprintf("https://%s-test.pgs.test%s", p.Users[0].Name, path)
}

func TestApiBasic(t *testing.T) {
	logger := slog.Default()
	dbpool := NewPgsDb(logger)
	bucketName := shared.GetAssetBucketName(dbpool.Users[0].ID)

	tt := []*ApiExample{
		{
			name:        "basic",
			path:        "/",
			want:        "hello world!",
			status:      http.StatusOK,
			contentType: "text/html",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/index.html": "hello world!",
				},
			},
		},
		{
			name:        "direct-file",
			path:        "/test.html",
			want:        "hello world!",
			status:      http.StatusOK,
			contentType: "text/html",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/test.html": "hello world!",
				},
			},
		},
		{
			name:        "subdir-301-redirect",
			path:        "/subdir",
			want:        `<a href="/subdir/">Moved Permanently</a>.`,
			status:      http.StatusMovedPermanently,
			contentType: "text/html; charset=utf-8",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/subdir/index.html": "hello world!",
				},
			},
		},
		{
			name:        "redirects-file-301",
			path:        "/anything",
			want:        `<a href="/about.html">Moved Permanently</a>.`,
			status:      http.StatusMovedPermanently,
			contentType: "text/html; charset=utf-8",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/_redirects": "/anything /about.html 301",
					"/test/about.html": "hello world!",
				},
			},
		},
		{
			name:        "subdir-direct",
			path:        "/subdir/index.html",
			want:        "hello world!",
			status:      http.StatusOK,
			contentType: "text/html",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/subdir/index.html": "hello world!",
				},
			},
		},
		{
			name:        "spa",
			path:        "/anything",
			want:        "hello world!",
			status:      http.StatusOK,
			contentType: "text/html",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/_redirects": "/* /index.html 200",
					"/test/index.html": "hello world!",
				},
			},
		},
		{
			name:        "not-found",
			path:        "/anything",
			want:        "404 not found",
			status:      http.StatusNotFound,
			contentType: "text/plain; charset=utf-8",

			storage: map[string]map[string]string{
				bucketName: {},
			},
		},
		{
			name:        "_redirects",
			path:        "/_redirects",
			want:        "404 not found",
			status:      http.StatusNotFound,
			contentType: "text/plain; charset=utf-8",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/_redirects": "/ok /index.html 200",
				},
			},
		},
		{
			name:        "_headers",
			path:        "/_headers",
			want:        "404 not found",
			status:      http.StatusNotFound,
			contentType: "text/plain; charset=utf-8",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/_headers": "/templates/index.html\n\tX-Frame-Options: DENY",
				},
			},
		},
		{
			name:        "_pgs_ignore",
			path:        "/_pgs_ignore",
			want:        "404 not found",
			status:      http.StatusNotFound,
			contentType: "text/plain; charset=utf-8",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/_pgs_ignore": "# nothing",
				},
			},
		},
		{
			name:        "not-found-custom",
			path:        "/anything",
			want:        "boom!",
			status:      http.StatusNotFound,
			contentType: "text/html",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/404.html": "boom!",
				},
			},
		},
		{
			name:        "images",
			path:        "/profile.jpg",
			want:        "image",
			status:      http.StatusOK,
			contentType: "image/jpeg",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/profile.jpg": "image",
				},
			},
		},
		{
			name:        "redirects-query-param",
			path:        "/anything?query=param",
			want:        `<a href="/about.html?query=param">Moved Permanently</a>.`,
			wantUrl:     "/about.html?query=param",
			status:      http.StatusMovedPermanently,
			contentType: "text/html; charset=utf-8",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/_redirects": "/anything /about.html 301",
					"/test/about.html": "hello world!",
				},
			},
		},
		{
			name: "conditional-if-modified-since-future",
			path: "/test.html",
			reqHeaders: map[string]string{
				"If-Modified-Since": time.Now().UTC().Add(time.Hour).Format(http.TimeFormat),
			},
			want:        "",
			status:      http.StatusNotModified,
			contentType: "",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/test.html": "hello world!",
				},
			},
		},
		{
			name: "conditional-if-modified-since-past",
			path: "/test.html",
			reqHeaders: map[string]string{
				"If-Modified-Since": time.Now().UTC().Add(-time.Hour).Format(http.TimeFormat),
			},
			want:        "hello world!",
			status:      http.StatusOK,
			contentType: "text/html",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/test.html": "hello world!",
				},
			},
		},
		{
			name: "conditional-if-none-match-pass",
			path: "/test.html",
			reqHeaders: map[string]string{
				"If-None-Match": "\"static-etag-for-testing-purposes\"",
			},
			want:        "",
			status:      http.StatusNotModified,
			contentType: "",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/test.html": "hello world!",
				},
			},
		},
		{
			name: "conditional-if-none-match-fail",
			path: "/test.html",
			reqHeaders: map[string]string{
				"If-None-Match": "\"non-matching-etag\"",
			},
			want:        "hello world!",
			status:      http.StatusOK,
			contentType: "text/html",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/test.html": "hello world!",
				},
			},
		},
		{
			name: "conditional-if-none-match-and-if-modified-since",
			path: "/test.html",
			reqHeaders: map[string]string{
				// The matching etag should take precedence over the past mod time
				"If-None-Match":     "\"static-etag-for-testing-purposes\"",
				"If-Modified-Since": time.Now().UTC().Add(-time.Hour).Format(http.TimeFormat),
			},
			want:        "",
			status:      http.StatusNotModified,
			contentType: "",

			storage: map[string]map[string]string{
				bucketName: {
					"/test/test.html": "hello world!",
				},
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", dbpool.mkpath(tc.path), strings.NewReader(""))
			for key, val := range tc.reqHeaders {
				request.Header.Set(key, val)
			}
			responseRecorder := httptest.NewRecorder()

			memSt, err := storage.NewStorageMemory(tc.storage)
			if err != nil {
				t.Fatal(err)
			}
			st := newTestStorage(memSt)
			pubsub := NewPubsubChan()
			defer func() {
				_ = pubsub.Close()
			}()
			cfg := NewPgsConfig(logger, dbpool, st, pubsub)
			cfg.Domain = "pgs.test"
			router := NewWebRouter(cfg)
			router.ServeHTTP(responseRecorder, request)

			if responseRecorder.Code != tc.status {
				t.Errorf("Want status '%d', got '%d'", tc.status, responseRecorder.Code)
			}

			ct := responseRecorder.Header().Get("content-type")
			if ct != tc.contentType {
				t.Errorf("Want content type '%s', got '%s'", tc.contentType, ct)
			}

			body := strings.TrimSpace(responseRecorder.Body.String())
			if body != tc.want {
				t.Errorf("Want '%s', got '%s'", tc.want, body)
			}

			if tc.wantUrl != "" {
				location, err := responseRecorder.Result().Location()
				if err != nil {
					t.Errorf("err: %s", err.Error())
				}
				if location == nil {
					t.Error("no location header in response")
					return
				}
				if tc.wantUrl != location.String() {
					t.Errorf("Want '%s', got '%s'", tc.wantUrl, location.String())
				}
			}
		})
	}
}

func TestDirectoryListing(t *testing.T) {
	logger := slog.Default()
	dbpool := NewPgsDb(logger)
	bucketName := shared.GetAssetBucketName(dbpool.Users[0].ID)

	tt := []struct {
		name        string
		path        string
		status      int
		contentType string
		contains    []string
		notContains []string
		storage     map[string]map[string]string
	}{
		{
			name:        "directory-without-index-shows-listing",
			path:        "/docs/",
			status:      http.StatusOK,
			contentType: "text/html",
			contains: []string{
				"Index of /docs/",
				"readme.md",
				"guide.md",
			},
			storage: map[string]map[string]string{
				bucketName: {
					"/test/docs/readme.md": "# Readme",
					"/test/docs/guide.md":  "# Guide",
				},
			},
		},
		{
			name:        "directory-with-index-serves-index",
			path:        "/docs/",
			status:      http.StatusOK,
			contentType: "text/html",
			contains:    []string{"hello world!"},
			notContains: []string{"Index of"},
			storage: map[string]map[string]string{
				bucketName: {
					"/test/docs/index.html": "hello world!",
					"/test/docs/readme.md":  "# Readme",
				},
			},
		},
		{
			name:        "root-directory-without-index-shows-listing",
			path:        "/",
			status:      http.StatusOK,
			contentType: "text/html",
			contains: []string{
				"Index of /",
				"style.css",
			},
			storage: map[string]map[string]string{
				bucketName: {
					"/test/style.css": "body {}",
				},
			},
		},
		{
			name:        "nested-directory-shows-parent-link",
			path:        "/assets/images/",
			status:      http.StatusOK,
			contentType: "text/html",
			contains: []string{
				"Index of /assets/images/",
				`href="../"`,
				"logo.png",
			},
			storage: map[string]map[string]string{
				bucketName: {
					"/test/assets/images/logo.png": "png data",
				},
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", dbpool.mkpath(tc.path), strings.NewReader(""))
			responseRecorder := httptest.NewRecorder()

			memSt, err := storage.NewStorageMemory(tc.storage)
			if err != nil {
				t.Fatal(err)
			}
			st := newTestStorage(memSt)
			pubsub := NewPubsubChan()
			defer func() {
				_ = pubsub.Close()
			}()
			cfg := NewPgsConfig(logger, dbpool, st, pubsub)
			cfg.Domain = "pgs.test"
			router := NewWebRouter(cfg)
			router.ServeHTTP(responseRecorder, request)

			if responseRecorder.Code != tc.status {
				t.Errorf("Want status '%d', got '%d'", tc.status, responseRecorder.Code)
			}

			ct := responseRecorder.Header().Get("content-type")
			if ct != tc.contentType {
				t.Errorf("Want content type '%s', got '%s'", tc.contentType, ct)
			}

			body := responseRecorder.Body.String()
			for _, want := range tc.contains {
				if !strings.Contains(body, want) {
					t.Errorf("Want body to contain '%s', got '%s'", want, body)
				}
			}
			for _, notWant := range tc.notContains {
				if strings.Contains(body, notWant) {
					t.Errorf("Want body to NOT contain '%s', got '%s'", notWant, body)
				}
			}
		})
	}
}

// minimalJPEG returns a minimal valid 1x1 JPEG image.
func minimalJPEG(t *testing.T) []byte {
	data, err := os.ReadFile("splash.jpg")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestImageManipulation(t *testing.T) {
	if imgproxyURL == "" {
		t.Skip("imgproxy container not available")
	}

	logger := slog.Default()
	dbpool := NewPgsDb(logger)
	bucketName := shared.GetAssetBucketName(dbpool.Users[0].ID)

	tt := []struct {
		name        string
		path        string
		status      int
		contentType string
		storage     map[string]map[string]string
	}{
		{
			name:        "root-img",
			path:        "/app.jpg/s:500/rt:90",
			status:      http.StatusOK,
			contentType: "image/jpeg",
			storage: map[string]map[string]string{
				bucketName: {
					"/test/app.jpg": string(minimalJPEG(t)),
				},
			},
		},
		{
			name:        "root-subdir-img",
			path:        "/subdir/app.jpg/rt:90/s:500",
			status:      http.StatusOK,
			contentType: "image/jpeg",
			storage: map[string]map[string]string{
				bucketName: {
					"/test/subdir/app.jpg": string(minimalJPEG(t)),
				},
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", dbpool.mkpath(tc.path), strings.NewReader(""))
			responseRecorder := httptest.NewRecorder()

			memSt, err := storage.NewStorageMemory(tc.storage)
			if err != nil {
				t.Fatal(err)
			}
			st := newTestStorage(memSt)
			pubsub := NewPubsubChan()
			defer func() {
				_ = pubsub.Close()
			}()
			cfg := NewPgsConfig(logger, dbpool, st, pubsub)
			cfg.Domain = "pgs.test"
			router := NewWebRouter(cfg)
			router.ServeHTTP(responseRecorder, request)

			if responseRecorder.Code != tc.status {
				t.Errorf("Want status '%d', got '%d'", tc.status, responseRecorder.Code)
			}

			ct := responseRecorder.Header().Get("content-type")
			if ct != tc.contentType {
				t.Errorf("Want content type '%s', got '%s'", tc.contentType, ct)
			}

			// With a real imgproxy, the response is binary image data.
			// Verify we got some content back (not empty).
			body := responseRecorder.Body.Bytes()
			if len(body) == 0 {
				t.Error("Expected non-empty image response body")
			}
		})
	}
}
