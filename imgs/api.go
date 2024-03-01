package imgs

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	_ "net/http/pprof"

	"github.com/gorilla/feeds"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/pgs"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
)

type PostPageData struct {
	ImgURL template.URL
}

type BlogPageData struct {
	Site      *shared.SitePageData
	PageTitle string
	URL       template.URL
	Username  string
	Posts     []template.URL
}

var Space = "imgs"

func ImgsListHandler(w http.ResponseWriter, r *http.Request) {
	username := shared.GetUsernameFromRequest(r)
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	cfg := shared.GetCfg(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Info("blog not found", "username", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	var posts []*db.Post
	pager := &db.Pager{Num: 1000, Page: 0}
	p, err := dbpool.FindPostsForUser(pager, user.ID, Space)
	posts = p.Data

	if err != nil {
		logger.Error(err.Error())
		http.Error(w, "could not fetch posts for blog", http.StatusInternalServerError)
		return
	}

	ts, err := shared.RenderTemplate(cfg, []string{
		cfg.StaticPath("html/imgs.page.tmpl"),
	})

	if err != nil {
		logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	curl := shared.CreateURLFromRequest(cfg, r)
	postCollection := make([]template.URL, 0, len(posts))
	for _, post := range posts {
		url := cfg.ImgURL(curl, post.Username, post.Slug)
		postCollection = append(postCollection, template.URL(url))
	}

	data := BlogPageData{
		Site:      cfg.GetSiteData(),
		PageTitle: fmt.Sprintf("%s imgs", username),
		URL:       template.URL(cfg.FullBlogURL(curl, username)),
		Username:  username,
		Posts:     postCollection,
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func ImgsRssHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	cfg := shared.GetCfg(r)

	pager, err := dbpool.FindAllPosts(&db.Pager{Num: 25, Page: 0}, Space)
	if err != nil {
		logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := template.ParseFiles(cfg.StaticPath("html/rss.page.tmpl"))
	if err != nil {
		logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	feed := &feeds.Feed{
		Title:       fmt.Sprintf("%s imgs feed", cfg.Domain),
		Link:        &feeds.Link{Href: cfg.HomeURL()},
		Description: fmt.Sprintf("%s latest image", cfg.Domain),
		Author:      &feeds.Author{Name: cfg.Domain},
		Created:     time.Now(),
	}

	curl := shared.CreateURLFromRequest(cfg, r)

	var feedItems []*feeds.Item
	for _, post := range pager.Data {
		var tpl bytes.Buffer
		data := &PostPageData{
			ImgURL: template.URL(cfg.ImgURL(curl, post.Username, post.Filename)),
		}
		if err := ts.Execute(&tpl, data); err != nil {
			continue
		}

		realUrl := cfg.FullPostURL(curl, post.Username, post.Filename)
		if !curl.Subdomain && !curl.UsernameInRoute {
			realUrl = fmt.Sprintf("%s://%s%s", cfg.Protocol, r.Host, realUrl)
		}

		item := &feeds.Item{
			Id:          realUrl,
			Title:       post.Title,
			Link:        &feeds.Link{Href: realUrl},
			Content:     tpl.String(),
			Created:     *post.PublishAt,
			Updated:     *post.UpdatedAt,
			Description: post.Description,
			Author:      &feeds.Author{Name: post.Username},
		}

		if post.Description != "" {
			item.Description = post.Description
		}

		feedItems = append(feedItems, item)
	}
	feed.Items = feedItems

	rss, err := feed.ToAtom()
	if err != nil {
		logger.Error(err.Error())
		http.Error(w, "Could not generate atom rss feed", http.StatusInternalServerError)
	}

	w.Header().Add("Content-Type", "application/atom+xml")
	_, err = w.Write([]byte(rss))
	if err != nil {
		logger.Error(err.Error())
	}
}

func anyPerm(proj *db.Project) bool {
	return true
}

func ImgRequest(w http.ResponseWriter, r *http.Request) {
	subdomain := shared.GetSubdomain(r)
	cfg := shared.GetCfg(r)
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	username := shared.GetUsernameFromRequest(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Info("rss feed not found", "user", username)
		http.Error(w, "rss feed not found", http.StatusNotFound)
		return
	}

	var imgOpts string
	var slug string
	if !cfg.IsSubdomains() || subdomain == "" {
		slug, _ = url.PathUnescape(shared.GetField(r, 1))
		imgOpts, _ = url.PathUnescape(shared.GetField(r, 2))
	} else {
		slug, _ = url.PathUnescape(shared.GetField(r, 0))
		imgOpts, _ = url.PathUnescape(shared.GetField(r, 1))
	}

	opts, err := storage.UriToImgProcessOpts(imgOpts)
	if err != nil {
		errMsg := fmt.Sprintf("error processing img options: %s", err.Error())
		logger.Info(errMsg)
		http.Error(w, errMsg, http.StatusUnprocessableEntity)
		return
	}

	// set default quality for web optimization
	if opts.Quality == 0 {
		opts.Quality = 80
	}

	// set default format to be webp
	if opts.Ext == "" {
		opts.Ext = "webp"
	}

	ext := filepath.Ext(slug)
	// Files can contain periods.  `filepath.Ext` is greedy and will clip the last period in the slug
	// and call that a file extension so we want to be explicit about what
	// file extensions we clip here
	for _, fext := range cfg.AllowedExt {
		if ext == fext {
			// users might add the file extension when requesting an image
			// but we want to remove that
			slug = shared.SanitizeFileExt(slug)
			break
		}
	}

	post, err := FindImgPost(r, user, slug)
	if err != nil {
		errMsg := fmt.Sprintf("image not found %s/%s", user.Name, slug)
		logger.Info(errMsg)
		http.Error(w, errMsg, http.StatusNotFound)
		return
	}

	fname := post.Filename
	pgs.ServeAsset(fname, opts, true, anyPerm, w, r)
}

func FindImgPost(r *http.Request, user *db.User, slug string) (*db.Post, error) {
	dbpool := shared.GetDB(r)
	return dbpool.FindPostWithSlug(slug, user.ID, Space)
}

func redirectHandler(w http.ResponseWriter, r *http.Request) {
	username := shared.GetUsernameFromRequest(r)
	url := fmt.Sprintf("https://%s.prose.sh/i", username)
	http.Redirect(w, r, url, http.StatusMovedPermanently)
}

func createMainRoutes(staticRoutes []shared.Route) []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "/check", shared.CheckHandler),
		shared.NewRoute("GET", "/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://prose.sh", http.StatusMovedPermanently)
		}),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		shared.NewRoute("GET", "/rss", ImgsRssHandler),
		shared.NewRoute("GET", "/rss.xml", ImgsRssHandler),
		shared.NewRoute("GET", "/atom.xml", ImgsRssHandler),
		shared.NewRoute("GET", "/feed.xml", ImgsRssHandler),

		shared.NewRoute("GET", "/([^/]+)", redirectHandler),
		shared.NewRoute("GET", "/([^/]+)/o/([^/]+)", ImgRequest),
		shared.NewRoute("GET", "/([^/]+)/([^/]+)", ImgRequest),
		shared.NewRoute("GET", "/([^/]+)/([^/]+)/(.+)", ImgRequest),
	)

	return routes
}

func createSubdomainRoutes(staticRoutes []shared.Route) []shared.Route {
	routes := []shared.Route{}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		shared.NewRoute("GET", "/", redirectHandler),
		shared.NewRoute("GET", "/o/([^/]+)", ImgRequest),
		shared.NewRoute("GET", "/([^/]+)", ImgRequest),
		shared.NewRoute("GET", "/([^/]+)/(.+)", ImgRequest),
	)

	return routes
}

func StartApiServer() {
	cfg := NewConfigSite()
	logger := cfg.Logger

	db := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer db.Close()

	var st storage.StorageServe
	var err error
	if cfg.MinioURL == "" {
		st, err = storage.NewStorageFS(cfg.StorageDir)
	} else {
		st, err = storage.NewStorageMinio(cfg.MinioURL, cfg.MinioUser, cfg.MinioPass)
	}

	if err != nil {
		logger.Error(err.Error())
	}

	staticRoutes := []shared.Route{}
	if cfg.Debug {
		staticRoutes = shared.CreatePProfRoutes(staticRoutes)
	}

	mainRoutes := createMainRoutes(staticRoutes)
	subdomainRoutes := createSubdomainRoutes(staticRoutes)

	httpCtx := &shared.HttpCtx{
		Cfg:     cfg,
		Dbpool:  db,
		Storage: st,
		Logger:  logger,
	}
	handler := shared.CreateServe(mainRoutes, subdomainRoutes, httpCtx)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Info(
		"Starting server on port",
		"port", cfg.Port,
		"domain", cfg.Domain,
		"email", cfg.Email,
	)

	logger.Error(http.ListenAndServe(portStr, router).Error())
}
