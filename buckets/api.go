package buckets

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"time"

	_ "net/http/pprof"

	gocache "github.com/patrickmn/go-cache"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"go.uber.org/zap"
)

type PageData struct {
	Site shared.SitePageData
}

type PostItemData struct {
	BlogURL      template.URL
	URL          template.URL
	ImgURL       template.URL
	PublishAtISO string
	PublishAt    string
	Caption      string
}

type BlogPageData struct {
	Site      shared.SitePageData
	PageTitle string
	URL       template.URL
	RSSURL    template.URL
	Username  string
	Readme    *ReadmeTxt
	Header    *HeaderTxt
	Posts     []*PostItemData
	HasFilter bool
}

type PostPageData struct {
	Site         shared.SitePageData
	PageTitle    string
	URL          template.URL
	BlogURL      template.URL
	Slug         string
	Title        string
	Caption      string
	Contents     template.HTML
	Text         string
	Username     string
	BlogName     string
	PublishAtISO string
	PublishAt    string
	Tags         []Link
	ImgURL       template.URL
	PrevPage     template.URL
	NextPage     template.URL
}

type TransparencyPageData struct {
	Site      shared.SitePageData
	Analytics *db.Analytics
}

type Link struct {
	URL  template.URL
	Text string
}

type HeaderTxt struct {
	Title    string
	Bio      string
	Nav      []Link
	HasLinks bool
}

type ReadmeTxt struct {
	HasText  bool
	Contents template.HTML
}

func GetBlogName(username string) string {
	return username
}

type ImgHandler struct {
	Username  string
	Subdomain string
	Slug      string
	Cfg       *shared.ConfigSite
	Dbpool    db.DB
	Storage   storage.ObjectStorage
	Logger    *zap.SugaredLogger
	Cache     *gocache.Cache
	Img       *shared.ImgOptimizer
	// We should try to use the optimized image if it's available
	// not all images are optimized so this flag isn't enough
	// because we also need to check the mime type
	UseOptimized bool
}

func assetHandler(w http.ResponseWriter, h *ImgHandler) {
	user, err := h.Dbpool.FindUserForName(h.Username)
	if err != nil {
		h.Logger.Infof("blog not found: %s", h.Username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	post, err := h.Dbpool.FindPostWithSlug(h.Slug, user.ID, h.Cfg.Space)
	if err != nil {
		h.Logger.Infof("image not found %s/%s", h.Username, h.Slug)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_, err = h.Dbpool.AddViewCount(post.ID)
	if err != nil {
		h.Logger.Error(err)
	}

	bucket, err := h.Storage.GetBucket(user.ID)
	if err != nil {
		h.Logger.Infof("bucket not found %s/%s", h.Username, post.Filename)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	contentType := post.MimeType
	fname := post.Filename

	contents, err := h.Storage.GetFile(bucket, fname)
	if err != nil {
		h.Logger.Infof(
			"file not found %s/%s in storage (bucket: %s, name: %s)",
			h.Username,
			post.Filename,
			bucket.Name,
			fname,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer contents.Close()

	w.Header().Add("Content-Type", contentType)

	if err != nil {
		h.Logger.Error(err)
	}
}

func assetRequest(w http.ResponseWriter, r *http.Request) {
	username := shared.GetUsernameFromRequest(r)
	subdomain := shared.GetSubdomain(r)
	cfg := shared.GetCfg(r)

	var dimes string
	var slug string
	if !cfg.IsSubdomains() || subdomain == "" {
		slug, _ = url.PathUnescape(shared.GetField(r, 1))
		dimes, _ = url.PathUnescape(shared.GetField(r, 2))
	} else {
		slug, _ = url.PathUnescape(shared.GetField(r, 0))
		dimes, _ = url.PathUnescape(shared.GetField(r, 1))
	}

	// users might add the file extension when requesting an image
	// but we want to remove that
	slug = shared.SanitizeFileExt(slug)

	dbpool := shared.GetDB(r)
	st := shared.GetStorage(r)
	logger := shared.GetLogger(r)
	cache := shared.GetCache(r)

	assetHandler(w, &ImgHandler{
		Username:     username,
		Subdomain:    subdomain,
		Slug:         slug,
		Cfg:          cfg,
		Dbpool:       dbpool,
		Storage:      st,
		Logger:       logger,
		Cache:        cache,
		Img:          shared.NewImgOptimizer(logger, dimes),
		UseOptimized: true,
	})
}

func transparencyHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	cfg := shared.GetCfg(r)

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

func createStaticRoutes() []shared.Route {
	return []shared.Route{
		shared.NewRoute("GET", "/main.css", shared.ServeFile("main.css", "text/css")),
		shared.NewRoute("GET", "/imgs.css", shared.ServeFile("imgs.css", "text/css")),
		shared.NewRoute("GET", "/card.png", shared.ServeFile("card.png", "image/png")),
		shared.NewRoute("GET", "/favicon-16x16.png", shared.ServeFile("favicon-16x16.png", "image/png")),
		shared.NewRoute("GET", "/favicon-32x32.png", shared.ServeFile("favicon-32x32.png", "image/png")),
		shared.NewRoute("GET", "/apple-touch-icon.png", shared.ServeFile("apple-touch-icon.png", "image/png")),
		shared.NewRoute("GET", "/favicon.ico", shared.ServeFile("favicon.ico", "image/x-icon")),
		shared.NewRoute("GET", "/robots.txt", shared.ServeFile("robots.txt", "text/plain")),
	}
}

func createMainRoutes(staticRoutes []shared.Route) []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "/", shared.CreatePageHandler("html/marketing.page.tmpl")),
		shared.NewRoute("GET", "/ops", shared.CreatePageHandler("html/ops.page.tmpl")),
		shared.NewRoute("GET", "/privacy", shared.CreatePageHandler("html/privacy.page.tmpl")),
		shared.NewRoute("GET", "/help", shared.CreatePageHandler("html/help.page.tmpl")),
		shared.NewRoute("GET", "/transparency", transparencyHandler),
		shared.NewRoute("GET", "/check", shared.CheckHandler),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	return routes
}

func createSubdomainRoutes(staticRoutes []shared.Route) []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "*", assetRequest),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	return routes
}

func StartApiServer() {
	cfg := NewConfigSite()
	logger := cfg.Logger

	db := postgres.NewDB(&cfg.ConfigCms)
	defer db.Close()

	var st storage.ObjectStorage
	var err error
	if cfg.MinioURL == "" {
		st, err = storage.NewStorageFS(cfg.StorageDir)
	} else {
		st, err = storage.NewStorageMinio(cfg.MinioURL, cfg.MinioUser, cfg.MinioPass)
	}

	// cache resizing images since they are CPU-bound
	// we want to clear the cache since we are storing images
	// as []byte in-memory
	cache := gocache.New(2*time.Minute, 5*time.Minute)

	if err != nil {
		logger.Fatal(err)
	}

	staticRoutes := createStaticRoutes()

	if cfg.Debug {
		staticRoutes = shared.CreatePProfRoutes(staticRoutes)
	}

	mainRoutes := createMainRoutes(staticRoutes)
	subdomainRoutes := createSubdomainRoutes(staticRoutes)

	handler := shared.CreateServe(mainRoutes, subdomainRoutes, cfg, db, st, logger, cache)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Infof("Starting server on port %s", cfg.Port)
	logger.Infof("Subdomains enabled: %t", cfg.SubdomainsEnabled)
	logger.Infof("Domain: %s", cfg.Domain)
	logger.Infof("Email: %s", cfg.Email)

	logger.Fatal(http.ListenAndServe(portStr, router))
}
