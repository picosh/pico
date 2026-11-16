package pastes

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/shared/router"
)

// renderHandler serves post. Its test must first t.Chdir("../../..") so
// templates load.
func renderHandler(t *testing.T, post *db.Post) http.Handler {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	cfg := &Config{Domain: "pastes.test", Protocol: "https", Space: "pastes", Logger: logger}
	now := time.Now()
	post.Slug, post.Username, post.FileSize = post.Filename, "tester", len(post.Text)
	post.PublishAt, post.UpdatedAt = &now, &now
	web := &webRouter{cfg: cfg, db: &pasteDB{StubDB: stub.NewStubDB(logger), post: post}}
	return router.NewHostRouter(cfg.Domain, cfg.Space, web.mainMux(), web.sitesMux())
}

func get(h http.Handler, url string) *httptest.ResponseRecorder {
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, url, nil))
	return res
}

func checkRenderHeaders(t *testing.T, url string, res *httptest.ResponseRecorder) {
	t.Helper()
	csp := res.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "sandbox;") || strings.Contains(csp, "allow-") || strings.Contains(csp, "script-src") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("%s: CSP %q doesn't block scripts", url, csp)
	}
	for k, want := range map[string]string{
		"Content-Type":           "text/html; charset=utf-8",
		"X-Content-Type-Options": "nosniff",
		"X-Robots-Tag":           "noindex",
	} {
		if got := res.Header().Get(k); got != want {
			t.Errorf("%s: %s = %q, want %q", url, k, got, want)
		}
	}
}

func TestRenderMarkdownSanitizes(t *testing.T) {
	t.Chdir("../../..")
	text := "# Hello\n\nsome *text*\n\n<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[bad](javascript:alert(1))\n"
	h := renderHandler(t, &db.Post{Filename: "doc.md", Text: text})
	for _, url := range []string{
		"https://tester.pastes.test/render/doc.md",
		"https://tester.pastes.test/doc.md/render",
		"https://pastes.test/tester/doc.md/render",
		"https://pastes.test/render/tester/doc.md",
	} {
		res := get(h, url)
		body := res.Body.String()
		if res.Code != http.StatusOK || !strings.Contains(body, "<h1>Hello</h1>") || !strings.Contains(body, "<em>text</em>") {
			t.Fatalf("%s: %d %q", url, res.Code, body)
		}
		for _, bad := range []string{"<script>alert", "onerror", "javascript:"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: rendered page contains %q", url, bad)
			}
		}
		checkRenderHeaders(t, url, res)
	}
}

func TestRenderHTMLAsUploaded(t *testing.T) {
	t.Chdir("../../..")
	text := "<!doctype html><html><head><style>p{color:red}</style></head><body><p>hi</p><script>alert(1)</script></body></html>"
	h := renderHandler(t, &db.Post{Filename: "page.HTML", Text: text})
	url := "https://tester.pastes.test/render/page.HTML"
	res := get(h, url)
	if res.Code != http.StatusOK || res.Body.String() != text {
		t.Fatalf("%d %q", res.Code, res.Body.String())
	}
	checkRenderHeaders(t, url, res)
}

func TestRenderOnlyMarkdownAndHTML(t *testing.T) {
	t.Chdir("../../..")
	h := renderHandler(t, &db.Post{Filename: "main.go", Text: "package main"})
	if res := get(h, "https://tester.pastes.test/render/main.go"); res.Code != http.StatusNotFound || strings.Contains(res.Body.String(), "package main") {
		t.Fatalf("%d %q", res.Code, res.Body.String())
	}
}

func TestRenderExpiredPaste(t *testing.T) {
	t.Chdir("../../..")
	expired := time.Now().Add(-time.Minute)
	h := renderHandler(t, &db.Post{Filename: "doc.md", Text: "secret", ExpiresAt: &expired})
	if res := get(h, "https://tester.pastes.test/render/doc.md"); res.Code != http.StatusNotFound || strings.Contains(res.Body.String(), "secret") {
		t.Fatalf("%d %q", res.Code, res.Body.String())
	}
}

func TestPastePageLinksRenderedView(t *testing.T) {
	t.Chdir("../../..")
	for file, want := range map[string]bool{"doc.md": true, "page.html": true, "a.txt": false} {
		h := renderHandler(t, &db.Post{Filename: file, Text: "hi"})
		body := get(h, "https://tester.pastes.test/"+file).Body.String()
		if got := strings.Contains(body, "https://tester.pastes.test/render/"+file); got != want {
			t.Errorf("%s: rendered link %v, want %v", file, got, want)
		}
	}
}
