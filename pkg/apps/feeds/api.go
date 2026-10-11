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

func (web *webRouter) keepAliveHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := web.db
	logger := web.cfg.Logger
	postID, _ := url.PathUnescape(r.PathValue("id"))

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

func (web *webRouter) unsubHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := web.db
	logger := web.cfg.Logger
	postID, _ := url.PathUnescape(r.PathValue("id"))

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

type webRouter struct {
	cfg *Config
	db  DB
}

func (web *webRouter) mux() *http.ServeMux {
	cfg := web.cfg
	files := cfg.Static()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", files.Page("html/marketing.page.tmpl"))
	mux.HandleFunc("GET /keep-alive/{id...}", web.keepAliveHandler)
	mux.HandleFunc("GET /unsub/{id...}", web.unsubHandler)
	mux.Handle("GET /_metrics", promhttp.Handler())
	mux.HandleFunc("GET /main.css", files.File("main.css", "text/css"))
	mux.HandleFunc("GET /card.png", files.File("card.png", "image/png"))
	mux.HandleFunc("GET /favicon-16x16.png", files.File("favicon-16x16.png", "image/png"))
	mux.HandleFunc("GET /apple-touch-icon.png", files.File("apple-touch-icon.png", "image/png"))
	mux.HandleFunc("GET /favicon.ico", files.File("favicon.ico", "image/x-icon"))
	mux.HandleFunc("GET /robots.txt", files.File("robots.txt", "text/plain"))
	if cfg.Debug {
		router.CreatePProfRoutesMux(mux)
	}
	return mux
}

func StartApiServer() {
	cfg := NewConfig("feeds-web")
	db := NewDB(postgres.NewDB(cfg.DbURL, cfg.Logger))
	defer func() {
		_ = db.Close()
	}()
	logger := cfg.Logger

	// cron daily digest
	fetcher := NewFetcher(db, cfg)
	go fetcher.Loop()

	web := &webRouter{cfg: cfg, db: db}

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Info(
		"Starting server on port",
		"port", cfg.Port,
		"domain", cfg.Domain,
	)

	logger.Error(http.ListenAndServe(portStr, web.mux()).Error())
}
