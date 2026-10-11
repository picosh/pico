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
	cfg := NewConfigSite("feeds-test")
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
