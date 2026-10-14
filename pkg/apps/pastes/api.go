package pastes

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
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

func (web *webRouter) blogHandler(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	dbpool := web.db
	blogger := web.cfg.Logger
	logger := blogger.With("user", username)
	cfg := web.cfg

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

	ts, err := router.RenderTemplate(staticDir, []string{
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

	curl := shared.CreateURLFromRequest(cfg.Protocol, cfg.Domain, r)
	postCollection := make([]PostItemData, 0, len(posts))
	for _, post := range posts {
		p := PostItemData{
			URL:            template.URL(curl.FullPostURL(post.Username, post.Slug)),
			BlogURL:        template.URL(curl.FullBlogURL(post.Username)),
			Title:          post.Filename,
			PublishAt:      post.PublishAt.Format(time.DateOnly),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: shared.TimeAgo(post.UpdatedAt),
			UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
		}
		postCollection = append(postCollection, p)
	}

	data := BlogPageData{
		Site:      cfg.SiteData(),
		PageTitle: headerTxt.Title,
		URL:       template.URL(curl.FullBlogURL(username)),
		RSSURL:    template.URL(curl.RssBlogURL(username, "")),
		Header:    headerTxt,
		Username:  username,
		Posts:     postCollection,
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error("could not execute template", "err", err)
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

func (web *webRouter) postHandler(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	cfg := web.cfg
	slug, _ := url.PathUnescape(r.PathValue("slug"))

	dbpool := web.db
	blogger := web.cfg.Logger
	logger := blogger.With("slug", slug, "user", username)

	user, err := dbpool.FindUserByName(username)
	if err != nil {
		logger.Info("user not found")
		http.Error(w, "user not found", http.StatusNotFound)
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
			parsedText = "<pre>" + template.HTMLEscapeString(post.Text) + "</pre>"
		} else {
			parsedText, err = ParseText(post.Filename, post.Text)
			if err != nil {
				logger.Error("could not parse text", "err", err)
				parsedText = "<pre>" + template.HTMLEscapeString(post.Text) + "</pre>"
			}
			if post.ExpiresAt != nil {
				expiresAt = post.ExpiresAt.Format(time.DateOnly)
			}

			if post.Hidden {
				unlisted = true
			}
		}

		data = PostPageData{
			Site:         cfg.SiteData(),
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
			Site:         cfg.SiteData(),
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

	ts, err := router.RenderTemplate(staticDir, []string{
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

func (web *webRouter) postHandlerRaw(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	cfg := web.cfg
	slug, _ := url.PathUnescape(r.PathValue("slug"))

	dbpool := web.db
	blogger := web.cfg.Logger
	logger := blogger.With("slug", slug, "user", username)

	user, err := dbpool.FindUserByName(username)
	if err != nil {
		logger.Info("user not found")
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	logger = shared.LoggerWithUser(logger, user)

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

type webRouter struct {
	cfg *Config
	db  DB
}

func (web *webRouter) static(mux *http.ServeMux) {
	cfg := web.cfg
	files := cfg.Static()
	mux.HandleFunc("GET /main.css", files.File("main.css", "text/css"))
	mux.HandleFunc("GET /smol.css", files.File("smol.css", "text/css"))
	mux.HandleFunc("GET /syntax.css", files.File("syntax.css", "text/css"))
	mux.HandleFunc("GET /card.png", files.File("card.png", "image/png"))
	mux.HandleFunc("GET /favicon-16x16.png", files.File("favicon-16x16.png", "image/png"))
	mux.HandleFunc("GET /apple-touch-icon.png", files.File("apple-touch-icon.png", "image/png"))
	mux.HandleFunc("GET /favicon.ico", files.File("favicon.ico", "image/x-icon"))
	mux.HandleFunc("GET /robots.txt", files.File("robots.txt", "text/plain"))
	if cfg.Debug {
		router.CreatePProfRoutesMux(mux)
	}
}

// mainMux serves the marketing site and users' pastes below /{user}.
func (web *webRouter) mainMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", web.cfg.Static().Page("html/marketing.page.tmpl"))
	mux.HandleFunc("GET /check", router.CheckHandler(web.cfg.Domain, web.cfg.Space, web.db))
	mux.Handle("GET /_metrics", promhttp.Handler())
	web.static(mux)
	mux.HandleFunc("GET /{user}", web.blogHandler)
	mux.HandleFunc("GET /{user}/{slug}", web.postHandler)
	mux.HandleFunc("GET /{first}/{second}/{third}", web.rawRoute)
	return mux
}

// sitesMux serves a user's pastes on their subdomain.
func (web *webRouter) sitesMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", web.blogHandler)
	web.static(mux)
	mux.HandleFunc("GET /{slug}", web.postHandler)
	mux.HandleFunc("GET /{first}/{second}", web.rawRoute)
	return mux
}

// rawRoute serves a raw paste at .../{slug}/raw or raw/.../{slug}, with the
// user first on the main domain. The two overlap as patterns, so a path
// that fits both, like /raw/raw, is the first kind.
func (web *webRouter) rawRoute(w http.ResponseWriter, r *http.Request) {
	segs := []string{r.PathValue("first"), r.PathValue("second")}
	if third := r.PathValue("third"); third != "" {
		segs = append(segs, third)
	}
	switch {
	case segs[len(segs)-1] == "raw":
		segs = segs[:len(segs)-1]
	case segs[0] == "raw":
		segs = segs[1:]
	default:
		http.NotFound(w, r)
		return
	}
	if len(segs) == 2 {
		r.SetPathValue("user", segs[0])
	}
	r.SetPathValue("slug", segs[len(segs)-1])
	web.postHandlerRaw(w, r)
}

func StartApiServer() {
	cfg := NewConfig("pastes-web")
	db := NewDB(postgres.NewDB(cfg.DbURL, cfg.Logger))
	defer func() {
		_ = db.Close()
	}()
	logger := cfg.Logger

	go CronDeleteExpiredPosts(cfg, db)

	web := &webRouter{cfg: cfg, db: db}
	handler := router.NewHostRouter(cfg.Domain, cfg.Space, web.mainMux(), web.sitesMux())

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Info(
		"Starting server on port",
		"port", cfg.Port,
		"domain", cfg.Domain,
	)

	logger.Error(http.ListenAndServe(portStr, handler).Error())
}
