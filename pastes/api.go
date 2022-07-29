package pastes

import (
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"git.sr.ht/~erock/pico/wish/cms/db"
	"git.sr.ht/~erock/pico/wish/cms/db/postgres"
)

type PageData struct {
	Site SitePageData
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
	Site      SitePageData
	PageTitle string
	URL       template.URL
	RSSURL    template.URL
	Username  string
	Header    *HeaderTxt
	Posts     []PostItemData
}

type PostPageData struct {
	Site         SitePageData
	PageTitle    string
	URL          template.URL
	RawURL       template.URL
	BlogURL      template.URL
	Title        string
	Description  string
	Username     string
	BlogName     string
	Contents     template.HTML
	PublishAtISO string
	PublishAt    string
}

type TransparencyPageData struct {
	Site      SitePageData
	Analytics *db.Analytics
}

func renderTemplate(cfg *ConfigSite, templates []string) (*template.Template, error) {
	files := make([]string, len(templates))
	copy(files, templates)
	files = append(
		files,
		cfg.StaticPath("html/footer.partial.tmpl"),
		cfg.StaticPath("html/marketing-footer.partial.tmpl"),
		cfg.StaticPath("html/base.layout.tmpl"),
	)

	ts, err := template.ParseFiles(files...)
	if err != nil {
		return nil, err
	}
	return ts, nil
}

func createPageHandler(fname string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := GetLogger(r)
		cfg := GetCfg(r)
		ts, err := renderTemplate(cfg, []string{cfg.StaticPath(fname)})

		if err != nil {
			logger.Error(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := PageData{
			Site: *cfg.GetSiteData(),
		}
		err = ts.Execute(w, data)
		if err != nil {
			logger.Error(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
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

func GetUsernameFromRequest(r *http.Request) string {
	subdomain := GetSubdomain(r)
	cfg := GetCfg(r)

	if !cfg.IsSubdomains() || subdomain == "" {
		return GetField(r, 0)
	}
	return subdomain
}

func blogHandler(w http.ResponseWriter, r *http.Request) {
	username := GetUsernameFromRequest(r)
	dbpool := GetDB(r)
	logger := GetLogger(r)
	cfg := GetCfg(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("blog not found: %s", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}
	posts, err := dbpool.FindPostsForUser(user.ID, cfg.Space)
	if err != nil {
		logger.Error(err)
		http.Error(w, "could not fetch posts for blog", http.StatusInternalServerError)
		return
	}

	ts, err := renderTemplate(cfg, []string{
		cfg.StaticPath("html/blog.page.tmpl"),
	})

	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	headerTxt := &HeaderTxt{
		Title: GetBlogName(username),
		Bio:   "",
	}

	postCollection := make([]PostItemData, 0, len(posts))
	for _, post := range posts {
		p := PostItemData{
			URL:            template.URL(cfg.PostURL(post.Username, post.Filename)),
			BlogURL:        template.URL(cfg.BlogURL(post.Username)),
			Title:          FilenameToTitle(post.Filename, post.Title),
			PublishAt:      post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: TimeAgo(post.UpdatedAt),
			UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
		}
		postCollection = append(postCollection, p)
	}

	data := BlogPageData{
		Site:      *cfg.GetSiteData(),
		PageTitle: headerTxt.Title,
		URL:       template.URL(cfg.BlogURL(username)),
		RSSURL:    template.URL(cfg.RssBlogURL(username)),
		Header:    headerTxt,
		Username:  username,
		Posts:     postCollection,
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err)
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
	username := GetUsernameFromRequest(r)
	subdomain := GetSubdomain(r)
	cfg := GetCfg(r)

	var filename string
	if !cfg.IsSubdomains() || subdomain == "" {
		filename, _ = url.PathUnescape(GetField(r, 1))
	} else {
		filename, _ = url.PathUnescape(GetField(r, 0))
	}

	dbpool := GetDB(r)
	logger := GetLogger(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("blog not found: %s", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	blogName := GetBlogName(username)

	var data PostPageData
	post, err := dbpool.FindPostWithFilename(filename, user.ID, cfg.Space)
	if err == nil {
		parsedText, err := ParseText(post.Filename, post.Text)
		if err != nil {
			logger.Error(err)
		}

		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			PageTitle:    GetPostTitle(post),
			URL:          template.URL(cfg.PostURL(post.Username, post.Filename)),
			RawURL:       template.URL(cfg.RawPostURL(post.Username, post.Filename)),
			BlogURL:      template.URL(cfg.BlogURL(username)),
			Description:  post.Description,
			Title:        FilenameToTitle(post.Filename, post.Title),
			PublishAt:    post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO: post.PublishAt.Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Contents:     template.HTML(parsedText),
		}
	} else {
		logger.Infof("post not found %s/%s", username, filename)
		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			PageTitle:    "Paste not found",
			Description:  "Paste not found",
			Title:        "Paste not found",
			BlogURL:      template.URL(cfg.BlogURL(username)),
			PublishAt:    time.Now().Format("02 Jan, 2006"),
			PublishAtISO: time.Now().Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Contents:     "oops!  we can't seem to find this post.",
		}
	}

	ts, err := renderTemplate(cfg, []string{
		cfg.StaticPath("html/post.page.tmpl"),
	})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func postHandlerRaw(w http.ResponseWriter, r *http.Request) {
	username := GetUsernameFromRequest(r)
	subdomain := GetSubdomain(r)
	cfg := GetCfg(r)

	var filename string
	if !cfg.IsSubdomains() || subdomain == "" {
		filename, _ = url.PathUnescape(GetField(r, 1))
	} else {
		filename, _ = url.PathUnescape(GetField(r, 0))
	}

	dbpool := GetDB(r)
	logger := GetLogger(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("blog not found: %s", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	post, err := dbpool.FindPostWithFilename(filename, user.ID, cfg.Space)
	if err != nil {
		logger.Infof("post not found %s/%s", username, filename)
		http.Error(w, "post not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	_, err = w.Write([]byte(post.Text))
	if err != nil {
		logger.Error(err)
	}
}

func transparencyHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := GetDB(r)
	logger := GetLogger(r)
	cfg := GetCfg(r)

	analytics, err := dbpool.FindSiteAnalytics(cfg.Space)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := template.ParseFiles(
		cfg.StaticPath("html/transparency.page.tmpl"),
		cfg.StaticPath("html/footer.partial.tmpl"),
		cfg.StaticPath("html/marketing-footer.partial.tmpl"),
		cfg.StaticPath("html/base.layout.tmpl"),
	)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	data := TransparencyPageData{
		Site:      *cfg.GetSiteData(),
		Analytics: analytics,
	}
	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func serveFile(file string, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := GetLogger(r)
		cfg := GetCfg(r)

		contents, err := ioutil.ReadFile(cfg.StaticPath(fmt.Sprintf("public/%s", file)))
		if err != nil {
			logger.Error(err)
			http.Error(w, "file not found", 404)
		}
		w.Header().Add("Content-Type", contentType)

		_, err = w.Write(contents)
		if err != nil {
			logger.Error(err)
			http.Error(w, "server error", 500)
		}
	}
}

func createStaticRoutes() []Route {
	return []Route{
		NewRoute("GET", "/main.css", serveFile("main.css", "text/css")),
		NewRoute("GET", "/syntax.css", serveFile("syntax.css", "text/css")),
		NewRoute("GET", "/card.png", serveFile("card.png", "image/png")),
		NewRoute("GET", "/favicon-16x16.png", serveFile("favicon-16x16.png", "image/png")),
		NewRoute("GET", "/favicon-32x32.png", serveFile("favicon-32x32.png", "image/png")),
		NewRoute("GET", "/apple-touch-icon.png", serveFile("apple-touch-icon.png", "image/png")),
		NewRoute("GET", "/favicon.ico", serveFile("favicon.ico", "image/x-icon")),
		NewRoute("GET", "/robots.txt", serveFile("robots.txt", "text/plain")),
	}
}

func createMainRoutes(staticRoutes []Route) []Route {
	routes := []Route{
		NewRoute("GET", "/", createPageHandler("html/marketing.page.tmpl")),
		NewRoute("GET", "/spec", createPageHandler("html/spec.page.tmpl")),
		NewRoute("GET", "/ops", createPageHandler("html/ops.page.tmpl")),
		NewRoute("GET", "/privacy", createPageHandler("html/privacy.page.tmpl")),
		NewRoute("GET", "/help", createPageHandler("html/help.page.tmpl")),
		NewRoute("GET", "/transparency", transparencyHandler),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		NewRoute("GET", "/([^/]+)", blogHandler),
		NewRoute("GET", "/([^/]+)/([^/]+)", postHandler),
		NewRoute("GET", "/([^/]+)/([^/]+)/raw", postHandlerRaw),
		NewRoute("GET", "/raw/([^/]+)/([^/]+)", postHandlerRaw),
	)

	return routes
}

func createSubdomainRoutes(staticRoutes []Route) []Route {
	routes := []Route{
		NewRoute("GET", "/", blogHandler),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		NewRoute("GET", "/([^/]+)", postHandler),
		NewRoute("GET", "/([^/]+)/raw", postHandlerRaw),
		NewRoute("GET", "/raw/([^/]+)", postHandlerRaw),
	)

	return routes
}

func StartApiServer() {
	cfg := NewConfigSite()
	db := postgres.NewDB(&cfg.ConfigCms)
	defer db.Close()
	logger := cfg.Logger

	go CronDeleteExpiredPosts(cfg, db)

	staticRoutes := createStaticRoutes()
	mainRoutes := createMainRoutes(staticRoutes)
	subdomainRoutes := createSubdomainRoutes(staticRoutes)

	handler := CreateServe(mainRoutes, subdomainRoutes, cfg, db, logger)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Infof("Starting server on port %s", cfg.Port)
	logger.Infof("Subdomains enabled: %t", cfg.SubdomainsEnabled)
	logger.Infof("Domain: %s", cfg.Domain)
	logger.Infof("Email: %s", cfg.Email)

	logger.Fatal(http.ListenAndServe(portStr, router))
}
