package auth

import (
	"fmt"
	"net/http"

	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"go.uber.org/zap"
)

func loginHandler(w http.ResponseWriter, r *http.Request) {
	// dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	// cfg := shared.GetCfg(r)

	w.Header().Set("Content-Type", "text/plain")
	_, err := w.Write([]byte("message"))
	if err != nil {
		logger.Error(err)
	}
}

func createMainRoutes() []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "/login", loginHandler),
		// shared.NewRoute("GET", "/([^/]+)", blogHandler),
	}

	return routes
}

func handler(routes []shared.Route, cfg *AuthCfg) shared.ServeFn {
	return func(w http.ResponseWriter, r *http.Request) {
	}
}

type AuthCfg struct {
	Debug  bool
	Port   string
	DbURL  string
	Logger *zap.SugaredLogger
}

func StartApiServer() {
	debug := shared.GetEnv("AUTH_DEBUG", "0")
	cfg := &AuthCfg{
		DbURL:  shared.GetEnv("DATABASE_URL", ""),
		Logger: shared.CreateLogger(),
		Debug:  debug == "1",
		Port:   shared.GetEnv("AUTH_WEB_PORT", "3000"),
	}

	db := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer db.Close()

	logger := cfg.Logger
	routes := createMainRoutes()

	if cfg.Debug {
		routes = shared.CreatePProfRoutes(routes)
	}

	router := http.HandlerFunc(handler(routes, cfg))

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Infof("Starting server on port %s", cfg.Port)
	logger.Fatal(http.ListenAndServe(portStr, router))
}
