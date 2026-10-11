package pipe

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/picosh/pico/pkg/shared/router"
)

func TestRoutes(t *testing.T) {
	t.Chdir("../../..")
	t.Setenv("PICO_PIPE_ENABLED", "false")
	cfg := NewConfigSite("pipe-test")
	cfg.Logger = slog.New(slog.DiscardHandler)
	web := &webRouter{cfg: cfg, db: NewTestDB(cfg.Logger)}
	handler := router.NewHostRouter(cfg, web.mainMux(), web.sitesMux())

	site := "https://tester." + cfg.Domain
	main := "https://" + cfg.Domain
	for _, tt := range []struct {
		method string
		url    string
		status int
		cors   bool
	}{
		{method: http.MethodOptions, url: main + "/topic/a/b", status: http.StatusOK, cors: true},
		{method: http.MethodOptions, url: main + "/pubsub/a", status: http.StatusOK, cors: true},
		{method: http.MethodOptions, url: main + "/pipe/a", status: http.StatusOK, cors: true},
		{method: http.MethodPut, url: main + "/topic/a", status: http.StatusMethodNotAllowed},
		{method: http.MethodGet, url: main + "/favicon.ico", status: http.StatusOK},
		{method: http.MethodGet, url: main + "/missing", status: http.StatusNotFound},
		// Sites only serve the static files.
		{method: http.MethodGet, url: site + "/anim.js", status: http.StatusOK},
		{method: http.MethodGet, url: site + "/check", status: http.StatusNotFound},
		{method: http.MethodOptions, url: site + "/topic/a", status: http.StatusNotFound},
	} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(tt.method, tt.url, nil))
		cors := res.Header().Get("Access-Control-Allow-Origin") == "*"
		if res.Code != tt.status || cors != tt.cors {
			t.Errorf("%s %s = %d cors %v, want %d cors %v", tt.method, tt.url, res.Code, cors, tt.status, tt.cors)
		}
	}
}
