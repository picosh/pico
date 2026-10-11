package prose

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/shared/router"
	"github.com/picosh/pico/pkg/storage"
)

func TestRoutes(t *testing.T) {
	t.Chdir("../../..")
	t.Setenv("PICO_PIPE_ENABLED", "false")
	logger := slog.New(slog.DiscardHandler)
	user := &db.User{ID: "user-1", Name: "tester"}
	posts := stub.NewPostsDB(logger, user)
	cfg := NewConfig("prose-test")
	cfg.Logger = logger
	post := filehandlers.NewPost("hello.md", []byte("# hello"))
	post.UserID, post.Space, post.Title = user.ID, cfg.Space, "Hello"
	if _, err := posts.InsertPost(post); err != nil {
		t.Fatal(err)
	}
	st, err := storage.NewStorageMemory(map[string]map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	web := &webRouter{cfg: cfg, db: posts, storage: st}
	handler := router.NewHostRouter(cfg.Domain, cfg.Space, web.mainMux(), web.sitesMux())

	site := "https://tester." + cfg.Domain
	for _, tt := range []struct {
		url    string
		status int
		// body is what the response holds, "" for anything.
		body string
		// exact means the response is body and nothing else.
		exact bool
	}{
		{url: site + "/hello", status: http.StatusOK, body: "<html"},
		{url: site + "/hello.html", status: http.StatusOK, body: "<html"},
		{url: site + "/hello.md", status: http.StatusOK, body: "# hello"},
		{url: site + "/hello.lxt", status: http.StatusOK, body: "# hello"},
		{url: site + "/raw/hello", status: http.StatusOK, body: "# hello"},
		{url: site + "/missing", status: http.StatusNotFound},
		// Images are served from the user's bucket, which they don't have.
		{url: site + "/cat.png", status: http.StatusUnprocessableEntity, body: "bucket does not exist"},
		{url: site + "/cat.png/s:500", status: http.StatusUnprocessableEntity, body: "bucket does not exist"},
		// A site serves its own robots.txt, which this user hasn't uploaded.
		{url: site + "/robots.txt", status: http.StatusOK, exact: true},
		{url: "https://" + cfg.Domain + "/robots.txt", status: http.StatusOK, body: "User-agent"},
		{url: site + "/read.css", status: http.StatusOK},
		{url: "https://" + cfg.Domain + "/read.css", status: http.StatusOK},
		{url: "https://" + cfg.Domain + "/hello", status: http.StatusNotFound},
	} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, tt.url, nil))
		got := res.Body.String()
		if res.Code != tt.status || !strings.Contains(got, tt.body) || (tt.exact && got != tt.body) {
			t.Errorf("%s = %d %.80q, want %d %q", tt.url, res.Code, got, tt.status, tt.body)
		}
	}
}
