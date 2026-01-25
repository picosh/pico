package pastes

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type PageData struct {
	Site shared.SitePageData
}

type PostItemData struct {
	URL            template.URL
	BlogURL        template.URL
	Username       string
	Title          string
	Description    string
	PublishAtISO   string
	PublishAt      string
	UpdatedAtISO   string
	UpdatedTimeAgo string
	Padding        string
}

type BlogPageData struct {
	Site      shared.SitePageData
	PageTitle string
	URL       template.URL
	RSSURL    template.URL
	Username  string
	Header    *HeaderTxt
	Posts     []PostItemData
}

type PostPageData struct {
	Site         shared.SitePageData
	PageTitle    string
	URL          template.URL
	RawURL       template.URL
	BlogURL      template.URL
	Title        string
	Description  string
	Username     string
	BlogName     string
	Contents     template.HTML
	Text         string
	PublishAtISO string
	PublishAt    string
	ExpiresAt    string
	Unlisted     bool
}

type Link struct {
	URL  string
	Text string
}

type HeaderTxt struct {
	Title    string
	Bio      string
	Nav      []Link
	HasLinks bool
}

func blogHandler(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	dbpool := router.GetDB(r)
	blogger := router.GetLogger(r)
	logger := blogger.With("user", username)
	cfg := router.GetCfg(r)

	user, err := dbpool.FindUserByName(username)
	if err != nil {
		logger.Info("user not found")
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	logger = shared.LoggerWithUser(blogger, user)

	pager, err := dbpool.FindPostsByUser(&db.Pager{Num: 1000, Page: 0}, user.ID, cfg.Space)
	if err != nil {
		logger.Error("could not find posts for user", "err", err.Error())
		http.Error(w, "could not fetch posts for blog", http.StatusInternalServerError)
		return
	}

	posts := pager.Data

	ts, err := router.RenderTemplate(cfg, []string{
		cfg.StaticPath("html/blog.page.tmpl"),
	})

	if err != nil {
		logger.Error("could not render template", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	headerTxt := &HeaderTxt{
		Title: GetBlogName(username),
		Bio:   "",
	}

	curl := shared.CreateURLFromRequest(cfg, r)
	postCollection := make([]PostItemData, 0, len(posts))
	for _, post := range posts {
		p := PostItemData{
			URL:            template.URL(cfg.FullPostURL(curl, post.Username, post.Slug)),
			BlogURL:        template.URL(cfg.FullBlogURL(curl, post.Username)),
			Title:          post.Filename,
			PublishAt:      post.PublishAt.Format(time.DateOnly),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: shared.TimeAgo(post.UpdatedAt),
			UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
		}
		postCollection = append(postCollection, p)
	}

	data := BlogPageData{
		Site:      *cfg.GetSiteData(),
		PageTitle: headerTxt.Title,
		URL:       template.URL(cfg.FullBlogURL(curl, username)),
		RSSURL:    template.URL(cfg.RssBlogURL(curl, username, "")),
		Header:    headerTxt,
		Username:  username,
		Posts:     postCollection,
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error("could not execute tempalte", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func GetPostTitle(post *db.Post) string {
	if post.Description == "" {
		return post.Title
	}

	return fmt.Sprintf("%s: %s", post.Title, post.Description)
}

func GetBlogName(username string) string {
	return fmt.Sprintf("%s's pastes", username)
}

func postHandler(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	subdomain := router.GetSubdomain(r)
	cfg := router.GetCfg(r)

	var slug string
	if !cfg.IsSubdomains() || subdomain == "" {
		slug, _ = url.PathUnescape(router.GetField(r, 1))
	} else {
		slug, _ = url.PathUnescape(router.GetField(r, 0))
	}

	dbpool := router.GetDB(r)
	blogger := router.GetLogger(r)
	logger := blogger.With("slug", slug, "user", username)

	user, err := dbpool.FindUserByName(username)
	if err != nil {
		logger.Info("paste not found")
		http.Error(w, "paste not found", http.StatusNotFound)
		return
	}
	logger = shared.LoggerWithUser(logger, user)

	blogName := GetBlogName(username)

	var data PostPageData
	post, err := dbpool.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err == nil {
		logger = logger.With("filename", post.Filename)
		logger.Info("paste found")
		expiresAt := "never"
		unlisted := false
		parsedText := ""
		// we dont want to syntax highlight huge files
		if post.FileSize > 1*shared.MB {
			logger.Warn("paste too large to parse and apply syntax highlighting")
			parsedText = post.Text
		} else {
			parsedText, err = ParseText(post.Filename, post.Text)
			if err != nil {
				logger.Error("could not parse text", "err", err)
			}
			if post.ExpiresAt != nil {
				expiresAt = post.ExpiresAt.Format(time.DateOnly)
			}

			if post.Hidden {
				unlisted = true
			}
		}

		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			PageTitle:    post.Filename,
			URL:          template.URL(cfg.PostURL(post.Username, post.Slug)),
			RawURL:       template.URL(cfg.RawPostURL(post.Username, post.Slug)),
			BlogURL:      template.URL(cfg.BlogURL(username)),
			Description:  post.Description,
			Title:        post.Filename,
			PublishAt:    post.PublishAt.Format(time.DateOnly),
			PublishAtISO: post.PublishAt.Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Contents:     template.HTML(parsedText),
			Text:         post.Text,
			ExpiresAt:    expiresAt,
			Unlisted:     unlisted,
		}
	} else {
		logger.Info("paste not found")
		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			PageTitle:    "Paste not found",
			Description:  "Paste not found",
			Title:        "Paste not found",
			BlogURL:      template.URL(cfg.BlogURL(username)),
			PublishAt:    time.Now().Format(time.DateOnly),
			PublishAtISO: time.Now().Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Contents:     "oops!  we can't seem to find this post.",
			Text:         "oops!  we can't seem to find this post.",
			ExpiresAt:    "",
		}
	}

	ts, err := router.RenderTemplate(cfg, []string{
		cfg.StaticPath("html/post.page.tmpl"),
	})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	logger.Info("serving paste")
	err = ts.Execute(w, data)
	if err != nil {
		logger.Error("could not execute template", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func postHandlerRaw(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	subdomain := router.GetSubdomain(r)
	cfg := router.GetCfg(r)

	var slug string
	if !cfg.IsSubdomains() || subdomain == "" {
		slug, _ = url.PathUnescape(router.GetField(r, 1))
	} else {
		slug, _ = url.PathUnescape(router.GetField(r, 0))
	}

	dbpool := router.GetDB(r)
	blogger := router.GetLogger(r)
	logger := blogger.With("user", username, "slug", slug)

	user, err := dbpool.FindUserByName(username)
	if err != nil {
		logger.Info("user not found")
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	logger = shared.LoggerWithUser(blogger, user)

	post, err := dbpool.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err != nil {
		logger.Info("paste not found")
		http.Error(w, "paste not found", http.StatusNotFound)
		return
	}
	logger = logger.With("filename", post.Filename)
	logger.Info("raw paste found")

	w.Header().Set("Content-Type", "text/plain")
	_, err = w.Write([]byte(post.Text))
	if err != nil {
		logger.Error("write error", "err", err)
	}
}

func serveFile(file string, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := router.GetLogger(r)
		cfg := router.GetCfg(r)

		contents, err := os.ReadFile(cfg.StaticPath(fmt.Sprintf("public/%s", file)))
		if err != nil {
			logger.Error("could not read file", "err", err)
			http.Error(w, "file not found", 404)
		}
		w.Header().Add("Content-Type", contentType)

		_, err = w.Write(contents)
		if err != nil {
			logger.Error("could not write contents", "err", err)
			http.Error(w, "server error", 500)
		}
	}
}

func createStaticRoutes() []router.Route {
	return []router.Route{
		router.NewRoute("GET", "/main.css", serveFile("main.css", "text/css")),
		router.NewRoute("GET", "/smol.css", serveFile("smol.css", "text/css")),
		router.NewRoute("GET", "/syntax.css", serveFile("syntax.css", "text/css")),
		router.NewRoute("GET", "/card.png", serveFile("card.png", "image/png")),
		router.NewRoute("GET", "/favicon-16x16.png", serveFile("favicon-16x16.png", "image/png")),
		router.NewRoute("GET", "/favicon-32x32.png", serveFile("favicon-32x32.png", "image/png")),
		router.NewRoute("GET", "/apple-touch-icon.png", serveFile("apple-touch-icon.png", "image/png")),
		router.NewRoute("GET", "/favicon.ico", serveFile("favicon.ico", "image/x-icon")),
		router.NewRoute("GET", "/robots.txt", serveFile("robots.txt", "text/plain")),
	}
}

func createMainRoutes(staticRoutes []router.Route) []router.Route {
	routes := []router.Route{
		router.NewRoute("GET", "/", router.CreatePageHandler("html/marketing.page.tmpl")),
		router.NewRoute("GET", "/check", router.CheckHandler),
		router.NewRoute("GET", "/_metrics", promhttp.Handler().ServeHTTP),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		router.NewRoute("GET", "/([^/]+)", blogHandler),
		router.NewRoute("GET", "/([^/]+)/([^/]+)", postHandler),
		router.NewRoute("GET", "/([^/]+)/([^/]+)/raw", postHandlerRaw),
		router.NewRoute("GET", "/raw/([^/]+)/([^/]+)", postHandlerRaw),
	)

	return routes
}

func createSubdomainRoutes(staticRoutes []router.Route) []router.Route {
	routes := []router.Route{
		router.NewRoute("GET", "/", blogHandler),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		router.NewRoute("GET", "/([^/]+)", postHandler),
		router.NewRoute("GET", "/([^/]+)/raw", postHandlerRaw),
		router.NewRoute("GET", "/raw/([^/]+)", postHandlerRaw),
	)

	return routes
}

func StartApiServer() {
	cfg := NewConfigSite("pastes-web")
	db := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer func() {
		_ = db.Close()
	}()
	logger := cfg.Logger

	go CronDeleteExpiredPosts(cfg, db)

	staticRoutes := createStaticRoutes()

	if cfg.Debug {
		staticRoutes = router.CreatePProfRoutes(staticRoutes)
	}

	mainRoutes := createMainRoutes(staticRoutes)
	subdomainRoutes := createSubdomainRoutes(staticRoutes)

	apiConfig := &router.ApiConfig{
		Cfg:    cfg,
		Dbpool: db,
	}
	handler := router.CreateServe(mainRoutes, subdomainRoutes, apiConfig)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Info(
		"Starting server on port",
		"port", cfg.Port,
		"domain", cfg.Domain,
	)

	logger.Error(http.ListenAndServe(portStr, router).Error())
}
