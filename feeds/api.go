package feeds

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func keepAliveHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	postID, _ := url.PathUnescape(shared.GetField(r, 0))

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
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	postID, _ := url.PathUnescape(shared.GetField(r, 0))

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

func createMainRoutes(staticRoutes []shared.Route) []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "/", shared.CreatePageHandler("html/marketing.page.tmpl")),
		shared.NewRoute("GET", "/keep-alive/(.+)", keepAliveHandler),
		shared.NewRoute("GET", "/unsub/(.+)", unsubHandler),
		shared.NewRoute("GET", "/_metrics", promhttp.Handler().ServeHTTP),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	return routes
}

func createStaticRoutes() []shared.Route {
	return []shared.Route{
		shared.NewRoute("GET", "/main.css", shared.ServeFile("main.css", "text/css")),
		shared.NewRoute("GET", "/card.png", shared.ServeFile("card.png", "image/png")),
		shared.NewRoute("GET", "/favicon-16x16.png", shared.ServeFile("favicon-16x16.png", "image/png")),
		shared.NewRoute("GET", "/favicon-32x32.png", shared.ServeFile("favicon-32x32.png", "image/png")),
		shared.NewRoute("GET", "/apple-touch-icon.png", shared.ServeFile("apple-touch-icon.png", "image/png")),
		shared.NewRoute("GET", "/favicon.ico", shared.ServeFile("favicon.ico", "image/x-icon")),
		shared.NewRoute("GET", "/robots.txt", shared.ServeFile("robots.txt", "text/plain")),
	}
}

func StartApiServer() {
	cfg := NewConfigSite("feeds-web")
	db := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer db.Close()
	logger := cfg.Logger

	// cron daily digest
	fetcher := NewFetcher(db, cfg)
	go fetcher.Loop()

	staticRoutes := createStaticRoutes()

	if cfg.Debug {
		staticRoutes = shared.CreatePProfRoutes(staticRoutes)
	}

	mainRoutes := createMainRoutes(staticRoutes)

	apiConfig := &shared.ApiConfig{
		Cfg:    cfg,
		Dbpool: db,
	}
	handler := shared.CreateServe(mainRoutes, []shared.Route{}, apiConfig)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Info(
		"Starting server on port",
		"port", cfg.Port,
		"domain", cfg.Domain,
	)

	logger.Error(http.ListenAndServe(portStr, router).Error())
}
