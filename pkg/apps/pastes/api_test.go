package pastes

import (
	"context"
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
}

func (m *pasteDB) FindUserByName(string) (*db.User, error) {
	return &db.User{ID: "user-1", Name: "tester"}, nil
}

func (m *pasteDB) FindPostWithSlug(string, string, string) (*db.Post, error) {
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
	api := &router.ApiConfig{Cfg: cfg, Dbpool: dbpool}
	handler := router.CreateServeBasic(createSubdomainRoutes(nil), api.CreateCtx(context.Background(), "tester"))

	page := httptest.NewRecorder()
	handler(page, httptest.NewRequest(http.MethodGet, "https://tester.pastes.test/test.html", nil))
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
	handler(raw, httptest.NewRequest(http.MethodGet, "https://tester.pastes.test/test.html/raw", nil))
	if raw.Code != http.StatusOK || raw.Body.String() != text {
		t.Fatal("raw response does not preserve the uploaded text")
	}
}
