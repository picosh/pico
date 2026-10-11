package pastes

import (
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
)

type pasteDB struct {
	*stub.StubDB
	post *db.Post
	// user and slug are the last ones asked for.
	user, slug string
}

func (m *pasteDB) FindUserByName(name string) (*db.User, error) {
	m.user = name
	return &db.User{ID: "user-1", Name: name}, nil
}

func (m *pasteDB) FindPostWithSlug(slug string, _ string, _ string) (*db.Post, error) {
	m.slug = slug
	return m.post, nil
}

func TestLargePasteEscapesHTML(t *testing.T) {
	t.Chdir("../../..")
	logger := slog.New(slog.DiscardHandler)
	cfg := &shared.ConfigSite{
		Domain: "pastes.test", Protocol: "https", Space: "pastes", Logger: logger,
	}
	now := time.Now()
	text := strings.Repeat("x", shared.MB) + "\n<script>alert(1)</script>\n<img src=x onerror=alert(1)>\n& \" '"
	dbpool := &pasteDB{
		StubDB: stub.NewStubDB(logger),
		post: &db.Post{
			Filename: "test.html", Slug: "test.html", Username: "tester",
			Text: text, FileSize: len(text), PublishAt: &now, UpdatedAt: &now,
		},
	}
	web := &webRouter{cfg: cfg, db: dbpool}
	handler := router.NewHostRouter(cfg, web.mainMux(), web.sitesMux())

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "https://tester.pastes.test/test.html", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d", page.Code)
	}
	want := "<pre>" + template.HTMLEscapeString(text) + "</pre>"
	if !strings.Contains(page.Body.String(), want) {
		t.Fatal("page does not contain the escaped paste in a pre element")
	}
	if strings.Contains(page.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("page contains the uploaded script as executable HTML")
	}

	raw := httptest.NewRecorder()
	handler.ServeHTTP(raw, httptest.NewRequest(http.MethodGet, "https://tester.pastes.test/test.html/raw", nil))
	if raw.Code != http.StatusOK || raw.Body.String() != text {
		t.Fatal("raw response does not preserve the uploaded text")
	}
}

func TestRoutes(t *testing.T) {
	t.Chdir("../../..")
	logger := slog.New(slog.DiscardHandler)
	cfg := &shared.ConfigSite{
		Domain: "pastes.test", Protocol: "https", Space: "pastes", Logger: logger,
	}
	now := time.Now()
	dbpool := &pasteDB{
		StubDB: stub.NewStubDB(logger),
		post: &db.Post{
			Filename: "a.txt", Slug: "a.txt", Username: "tester",
			Text: "paste", FileSize: 5, PublishAt: &now, UpdatedAt: &now,
		},
	}
	web := &webRouter{cfg: cfg, db: dbpool}
	handler := router.NewHostRouter(cfg, web.mainMux(), web.sitesMux())

	for _, tt := range []struct {
		url    string
		status int
		raw    bool
		user   string
		slug   string
	}{
		{url: "https://pastes.test/tester/a.txt", status: http.StatusOK, user: "tester", slug: "a.txt"},
		{url: "https://pastes.test/tester/a.txt/raw", status: http.StatusOK, raw: true, user: "tester", slug: "a.txt"},
		{url: "https://pastes.test/raw/tester/a.txt", status: http.StatusOK, raw: true, user: "tester", slug: "a.txt"},
		// Both raw forms fit, and the trailing one wins.
		{url: "https://pastes.test/raw/tester/raw", status: http.StatusOK, raw: true, user: "raw", slug: "tester"},
		{url: "https://pastes.test/tester/a.txt/other", status: http.StatusNotFound},
		{url: "https://tester.pastes.test/a.txt", status: http.StatusOK, user: "tester", slug: "a.txt"},
		{url: "https://tester.pastes.test/a.txt/raw", status: http.StatusOK, raw: true, user: "tester", slug: "a.txt"},
		{url: "https://tester.pastes.test/raw/a.txt", status: http.StatusOK, raw: true, user: "tester", slug: "a.txt"},
		{url: "https://tester.pastes.test/a.txt/other", status: http.StatusNotFound},
	} {
		dbpool.user, dbpool.slug = "", ""
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, tt.url, nil))
		raw := res.Body.String() == "paste"
		if res.Code != tt.status || dbpool.user != tt.user || dbpool.slug != tt.slug || (res.Code == http.StatusOK && raw != tt.raw) {
			t.Errorf("%s = %d raw %v for %q/%q, want %d raw %v for %q/%q",
				tt.url, res.Code, raw, dbpool.user, dbpool.slug, tt.status, tt.raw, tt.user, tt.slug)
		}
	}
}
