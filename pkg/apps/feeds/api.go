package feeds

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func keepAliveHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := router.GetDB(r)
	logger := router.GetLogger(r)
	postID, _ := url.PathUnescape(router.GetField(r, 0))

	post, err := dbpool.FindPost(postID)
	if err != nil {
		logger.Error("post not found", "err", err)
		http.Error(w, "post not found", http.StatusNotFound)
		return
	}

	user, err := dbpool.FindUser(post.UserID)
	if err != nil {
		logger.Error("user not found", "err", err)
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	logger = shared.LoggerWithUser(logger, user)
	logger = logger.With("post", post.ID, "filename", post.Filename)

	now := time.Now()
	expiresAt := now.AddDate(0, 3, 0)
	post.ExpiresAt = &expiresAt
	_, err = dbpool.UpdatePost(post)
	if err != nil {
		logger.Error("could not update post", "err", err.Error())
		http.Error(w, "server error", 500)
		return
	}

	w.Header().Add("Content-Type", "text/plain")

	logger.Info(
		"Success! This feed will stay active until %s or by clicking the link in your digest email again",
		"expiresAt", now,
	)
	txt := fmt.Sprintf(
		"Success! This feed will stay active until %s or by clicking the link in your digest email again",
		now,
	)
	_, err = w.Write([]byte(txt))
	if err != nil {
		logger.Error("could not write to writer", "err", err.Error())
		http.Error(w, "server error", 500)
	}
}

func unsubHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := router.GetDB(r)
	logger := router.GetLogger(r)
	postID, _ := url.PathUnescape(router.GetField(r, 0))

	post, err := dbpool.FindPost(postID)
	if err != nil {
		logger.Error("post not found", "err", err)
		http.Error(w, "post not found", http.StatusNotFound)
		return
	}

	user, err := dbpool.FindUser(post.UserID)
	if err != nil {
		logger.Error("user not found", "err", err)
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	logger = shared.LoggerWithUser(logger, user)
	logger = logger.With("post", post.ID, "filename", post.Filename)

	logger.Info("unsubscribe")
	err = dbpool.RemovePosts([]string{post.ID})
	if err != nil {
		logger.Error("could not remove post", "err", err)
		http.Error(w, "could not remove post", http.StatusInternalServerError)
		return
	}

	txt := "Success! This feed digest post has been removed from our system."
	_, err = w.Write([]byte(txt))
	if err != nil {
		logger.Error("could not write to writer", "err", err)
		http.Error(w, "server error", 500)
	}
}

func createMainRoutes(staticRoutes []router.Route) []router.Route {
	routes := []router.Route{
		router.NewRoute("GET", "/", router.CreatePageHandler("html/marketing.page.tmpl")),
		router.NewRoute("GET", "/keep-alive/(.+)", keepAliveHandler),
		router.NewRoute("GET", "/unsub/(.+)", unsubHandler),
		router.NewRoute("GET", "/_metrics", promhttp.Handler().ServeHTTP),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	return routes
}

func createStaticRoutes() []router.Route {
	return []router.Route{
		router.NewRoute("GET", "/main.css", router.ServeFile("main.css", "text/css")),
		router.NewRoute("GET", "/card.png", router.ServeFile("card.png", "image/png")),
		router.NewRoute("GET", "/favicon-16x16.png", router.ServeFile("favicon-16x16.png", "image/png")),
		router.NewRoute("GET", "/favicon-32x32.png", router.ServeFile("favicon-32x32.png", "image/png")),
		router.NewRoute("GET", "/apple-touch-icon.png", router.ServeFile("apple-touch-icon.png", "image/png")),
		router.NewRoute("GET", "/favicon.ico", router.ServeFile("favicon.ico", "image/x-icon")),
		router.NewRoute("GET", "/robots.txt", router.ServeFile("robots.txt", "text/plain")),
	}
}

func StartApiServer() {
	cfg := NewConfigSite("feeds-web")
	db := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer func() {
		_ = db.Close()
	}()
	logger := cfg.Logger

	// cron daily digest
	fetcher := NewFetcher(db, cfg)
	go fetcher.Loop()

	staticRoutes := createStaticRoutes()

	if cfg.Debug {
		staticRoutes = router.CreatePProfRoutes(staticRoutes)
	}

	mainRoutes := createMainRoutes(staticRoutes)

	apiConfig := &router.ApiConfig{
		Cfg:    cfg,
		Dbpool: db,
	}
	handler := router.CreateServe(mainRoutes, []router.Route{}, apiConfig)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Info(
		"Starting server on port",
		"port", cfg.Port,
		"domain", cfg.Domain,
	)

	logger.Error(http.ListenAndServe(portStr, router).Error())
}
