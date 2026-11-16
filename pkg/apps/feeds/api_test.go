package feeds

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
)

func TestRoutes(t *testing.T) {
	t.Chdir("../../..")
	cfg := NewConfig("feeds-test")
	cfg.Logger = slog.New(slog.DiscardHandler)
	web := &webRouter{cfg: cfg, db: stub.NewPostsDB(cfg.Logger, &db.User{ID: "user-1"})}
	mux := web.mux()

	for _, tt := range []struct {
		url    string
		status int
	}{
		{url: "/", status: http.StatusOK},
		{url: "/main.css", status: http.StatusOK},
		{url: "/keep-alive/missing", status: http.StatusNotFound},
		{url: "/unsub/missing", status: http.StatusNotFound},
		{url: "/missing", status: http.StatusNotFound},
	} {
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "https://"+cfg.Domain+tt.url, nil))
		if res.Code != tt.status {
			t.Errorf("%s = %d, want %d", tt.url, res.Code, tt.status)
		}
	}
}

// The digest links act on feeds posts only, since every space shares the
// posts table.
func TestDigestLinksOnlyTouchFeedsPosts(t *testing.T) {
	t.Chdir("../../..")
	cfg := NewConfig("feeds-test")
	cfg.Logger = slog.New(slog.DiscardHandler)
	user := &db.User{ID: "user-1", Name: "tester"}
	posts := stub.NewPostsDB(cfg.Logger, user)
	blog, err := posts.InsertPost(&db.Post{UserID: user.ID, Space: "prose", Filename: "hello.md", Slug: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := posts.InsertPost(&db.Post{UserID: user.ID, Space: "feeds", Filename: "news.txt", Slug: "news"})
	if err != nil {
		t.Fatal(err)
	}
	mux := (&webRouter{cfg: cfg, db: posts}).mux()

	for _, path := range []string{"/keep-alive/", "/unsub/"} {
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "https://"+cfg.Domain+path+blog.ID, nil))
		if res.Code != http.StatusNotFound {
			t.Errorf("%s on a prose post = %d, want 404", path, res.Code)
		}
	}
	if _, err := posts.FindPost(blog.ID); err != nil {
		t.Fatalf("prose post was removed: %v", err)
	}

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "https://"+cfg.Domain+"/unsub/"+digest.ID, nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unsub of a feeds post = %d", res.Code)
	}
}
