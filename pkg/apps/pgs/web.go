package pgs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "net/http/pprof"

	"github.com/gorilla/feeds"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/httpcache"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
	"github.com/picosh/pico/pkg/storage"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type PgsCacheKey struct {
	Domain    string
	TxtPrefix string
}

func (c *PgsCacheKey) GetCacheKey(r *http.Request) string {
	subdomain := router.GetSubdomainFromRequest(r, c.Domain, c.TxtPrefix)
	// RFC 9111 §3: HEAD responses can be served from a stored GET response.
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	return subdomain + "__" + method + "__" + r.URL.RequestURI()
}

type PromCacheMetrics struct {
	Logger         *slog.Logger
	Cache          httpcache.Cacher
	CacheItems     prometheus.Gauge
	CacheSizeBytes prometheus.Gauge
	CacheHit       prometheus.Counter
	CacheMiss      prometheus.Counter
	UpstreamReq    prometheus.Counter
}

func NewPromCacheMetrics(logger *slog.Logger, reg prometheus.Registerer) *PromCacheMetrics {
	name := "pgs"
	auto := promauto.With(reg)
	return &PromCacheMetrics{
		Logger: logger,
		CacheItems: auto.NewGauge(prometheus.GaugeOpts{
			Namespace: name,
			Subsystem: "http_cache",
			Name:      "total_items",
			Help:      "Number of items in the http cache",
		}),
		CacheSizeBytes: auto.NewGauge(prometheus.GaugeOpts{
			Namespace: name,
			Subsystem: "http_cache",
			Name:      "total_size_bytes",
			Help:      "The total size of the http cache in bytes",
		}),
		CacheHit: auto.NewCounter(prometheus.CounterOpts{
			Namespace: name,
			Subsystem: "http_cache",
			Name:      "cache_hit_count",
			Help:      "The number of times there was a cache hit",
		}),
		CacheMiss: auto.NewCounter(prometheus.CounterOpts{
			Namespace: name,
			Subsystem: "http_cache",
			Name:      "cache_miss_count",
			Help:      "The number of times there was a cache miss",
		}),
		UpstreamReq: auto.NewCounter(prometheus.CounterOpts{
			Namespace: name,
			Subsystem: "http_cache",
			Name:      "upstream_request_count",
			Help:      "The number of times the upstream http server was requested",
		}),
	}
}
func (p *PromCacheMetrics) AddCacheItem(size float64) {
	p.CacheItems.Add(1)
	p.CacheSizeBytes.Add(size)
}
func (p *PromCacheMetrics) EvictCacheItem(key string, value []byte) {
	p.Logger.Info("evicting cache key", "key", key, "len_bytes", len(value))
	p.CacheItems.Add(-1)
	p.CacheSizeBytes.Add(-float64(len(value)))
}
func (p *PromCacheMetrics) AddCacheHit() {
	p.CacheHit.Add(1)
}
func (p *PromCacheMetrics) AddCacheMiss() {
	p.CacheMiss.Add(1)
}
func (p *PromCacheMetrics) AddUpstreamRequest() {
	p.UpstreamReq.Add(1)
}

func NewPgsHttpCache(cfg *PgsConfig, upstream http.Handler) (*httpcache.HttpCache, *SiteCache) {
	ttl := cfg.CacheTTL
	metrics := NewPromCacheMetrics(cfg.Logger, prometheus.DefaultRegisterer)
	cache := NewSiteCache(cfg.CacheMaxItems, ttl, metrics.EvictCacheItem)
	httpCache := &httpcache.HttpCache{
		Ttl:      ttl,
		Logger:   cfg.Logger,
		Upstream: upstream,
		Cache:    cache,
		CacheKey: &PgsCacheKey{
			Domain:    cfg.Domain,
			TxtPrefix: cfg.TxtPrefix,
		},
		CacheMetrics: metrics,
		MaxBodySize:  cfg.CacheMaxBodySize,
	}
	httpCache.Logger.Info(
		"httpcache initiated",
		"storageType", "expirable.LRU",
		"ttl", ttl,
		"maxItems", cfg.CacheMaxItems,
		"maxBodySize", cfg.CacheMaxBodySize,
	)
	return httpCache, cache
}

func StartApiServer(cfg *PgsConfig) {
	ctx := context.Background()

	router := NewWebRouter(cfg)
	httpCache, cache := NewPgsHttpCache(router.Cfg, router)
	go CacheMgmt(ctx, cfg, cache, router.clearSiteCache)

	portStr := fmt.Sprintf(":%s", cfg.WebPort)
	cfg.Logger.Info(
		"starting server on port",
		"port", cfg.WebPort,
		"domain", cfg.Domain,
	)
	err := http.ListenAndServe(portStr, httpCache)
	cfg.Logger.Error(
		"listen and serve",
		"err", err.Error(),
	)
}

type HasPerm = func(proj *db.Project) bool

type WebRouter struct {
	Cfg            *PgsConfig
	RootRouter     *http.ServeMux
	UserRouter     *http.ServeMux
	RedirectsCache *expirable.LRU[string, []*RedirectRule]
	HeadersCache   *expirable.LRU[string, []headerMatcher]
	SiteCache      *expirable.LRU[string, *siteInfo]
}

// siteInfo is what serving a site needs from the database and storage.
type siteInfo struct {
	user    *db.User
	project *db.Project
	bucket  storage.Bucket
	plus    *db.FeatureFlag
}

func (s *siteInfo) hasPicoPlus() bool {
	return s.plus != nil && s.plus.ExpiresAt.After(time.Now())
}

func NewWebRouter(cfg *PgsConfig) *WebRouter {
	return newWebRouter(cfg)
}

func newWebRouter(cfg *PgsConfig) *WebRouter {
	router := &WebRouter{
		Cfg:            cfg,
		RedirectsCache: expirable.NewLRU[string, []*RedirectRule](2048, nil, shared.CacheTimeout),
		HeadersCache:   expirable.NewLRU[string, []headerMatcher](2048, nil, shared.CacheTimeout),
		SiteCache:      expirable.NewLRU[string, *siteInfo](2048, nil, shared.CacheTimeout),
	}
	router.initRouters()
	return router
}

func (web *WebRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	subdomain := router.GetSubdomainFromRequest(r, web.Cfg.Domain, web.Cfg.TxtPrefix)
	if web.RootRouter == nil || web.UserRouter == nil {
		web.Cfg.Logger.Error("routers not initialized")
		http.Error(w, "routers not initialized", http.StatusInternalServerError)
		return
	}

	var mux *http.ServeMux
	if subdomain == "" {
		mux = web.RootRouter
	} else {
		mux = web.UserRouter
	}

	ctx := r.Context()
	ctx = context.WithValue(ctx, router.CtxSubdomainKey{}, subdomain)
	mux.ServeHTTP(w, r.WithContext(ctx))
}

// clearSiteCache drops what is cached about a site: its database records and
// its parsed _redirects and _headers. The key is the surrogate key that
// uploads and the CLI purge, or "*" for every site.
func (web *WebRouter) clearSiteCache(key string) {
	web.Cfg.Logger.Info("lru cache clear request", "key", key)
	if key == "*" {
		web.SiteCache.Purge()
		web.RedirectsCache.Purge()
		web.HeadersCache.Purge()
		return
	}
	web.SiteCache.Remove(key)
	web.RedirectsCache.Remove(filepath.Join(key, "_redirects"))
	web.HeadersCache.Remove(filepath.Join(key, "_headers"))
}

// findSite looks up the user, project, bucket and pico+ flag behind a
// subdomain. Only complete results are cached, so a site that is created or
// fixed later is found on its next request.
func (web *WebRouter) findSite(props *router.SubdomainProps, logger *slog.Logger) (*siteInfo, int, string) {
	key := getSurrogateKey(props.Username, props.ProjectName)
	if site, ok := web.SiteCache.Get(key); ok {
		return site, http.StatusOK, ""
	}

	user, err := web.Cfg.DB.FindUserByName(props.Username)
	if err != nil {
		logger.Info("user not found")
		return nil, http.StatusNotFound, "user not found"
	}
	project, err := web.Cfg.DB.FindProjectByName(user.ID, props.ProjectName)
	if err != nil {
		logger.Info("project not found")
		return nil, http.StatusNotFound, "project not found"
	}
	bucket, err := web.Cfg.Storage.GetBucket(shared.GetAssetBucketName(user.ID))
	if err != nil {
		logger.Error("bucket not found", "err", err)
		return nil, http.StatusNotFound, "bucket not found"
	}
	plus, _ := web.Cfg.DB.FindFeature(user.ID, "plus")

	site := &siteInfo{user: user, project: project, bucket: bucket, plus: plus}
	web.SiteCache.Add(key, site)
	return site, http.StatusOK, ""
}

func (web *WebRouter) initRouters() {
	// ensure legacy router is disabled
	// GODEBUG=httpmuxgo121=0

	// root domain
	rootRouter := http.NewServeMux()
	rootRouter.HandleFunc("GET /check", web.checkHandler)
	rootRouter.HandleFunc("GET /_metrics", func(w http.ResponseWriter, r *http.Request) {
		// we do *not* want to cache this handler
		w.Header().Set("cache-control", "no-store")
		promhttp.Handler().ServeHTTP(w, r)
	})
	rootRouter.Handle("GET /main.css", web.serveFile("main.css", "text/css"))
	rootRouter.Handle("GET /favicon-16x16.png", web.serveFile("favicon-16x16.png", "image/png"))
	rootRouter.Handle("GET /favicon.ico", web.serveFile("favicon.ico", "image/x-icon"))
	rootRouter.Handle("GET /robots.txt", web.serveFile("robots.txt", "text/plain"))

	rootRouter.Handle("GET /rss/updated", web.createRssHandler("updated_at"))
	rootRouter.Handle("GET /rss", web.createRssHandler("created_at"))
	rootRouter.Handle("GET /{$}", web.createPageHandler("html/marketing.page.tmpl"))
	web.RootRouter = rootRouter

	// subdomain or custom domains
	userRouter := http.NewServeMux()
	userRouter.HandleFunc("POST /pgs/login", web.handleLogin)
	userRouter.HandleFunc("POST /pgs/forms/{fname...}", web.handleAutoForm)
	userRouter.HandleFunc("GET /{fname...}", web.AssetRequest(WebPerm))
	userRouter.HandleFunc("GET /{$}", web.AssetRequest(WebPerm))
	web.UserRouter = userRouter
}

func (web *WebRouter) serveFile(file string, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := web.Cfg.Logger
		cfg := web.Cfg

		contents, err := os.ReadFile(cfg.StaticPath(fmt.Sprintf("public/%s", file)))
		if err != nil {
			logger.Error(
				"could not read file",
				"fname", file,
				"err", err.Error(),
			)
			http.Error(w, "file not found", 404)
		}

		w.Header().Add("Content-Type", contentType)

		_, err = w.Write(contents)
		if err != nil {
			logger.Error(
				"could not write http response",
				"file", file,
				"err", err.Error(),
			)
		}
	}
}

func renderTemplate(cfg *PgsConfig, templates []string) (*template.Template, error) {
	files := make([]string, len(templates))
	copy(files, templates)
	files = append(
		files,
		cfg.StaticPath("html/footer.partial.tmpl"),
		cfg.StaticPath("html/marketing-footer.partial.tmpl"),
		cfg.StaticPath("html/base.layout.tmpl"),
	)

	ts, err := template.New("base").ParseFiles(files...)
	if err != nil {
		return nil, err
	}
	return ts, nil
}

func (web *WebRouter) createPageHandler(fname string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := web.Cfg.Logger
		cfg := web.Cfg
		ts, err := renderTemplate(cfg, []string{cfg.StaticPath(fname)})

		if err != nil {
			logger.Error(
				"could not render template",
				"fname", fname,
				"err", err.Error(),
			)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := shared.PageData{
			Site: shared.SitePageData{Domain: template.URL(cfg.Domain), HomeURL: "/"},
		}
		err = ts.Execute(w, data)
		if err != nil {
			logger.Error(
				"could not execute template",
				"fname", fname,
				"err", err.Error(),
			)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func (web *WebRouter) checkHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := web.Cfg.DB
	cfg := web.Cfg
	logger := web.Cfg.Logger

	hostDomain := r.URL.Query().Get("domain")
	if hostDomain == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	appDomain := strings.Split(cfg.Domain, ":")[0]

	// we do *not* want to cache this handler
	w.Header().Set("cache-control", "no-store")

	if !strings.Contains(hostDomain, appDomain) {
		subdomain := router.GetCustomDomain(hostDomain, cfg.TxtPrefix)
		props, err := router.GetProjectFromSubdomain(subdomain)
		if err != nil {
			logger.Error(
				"could not get project from subdomain",
				"subdomain", subdomain,
				"err", err.Error(),
			)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		u, err := dbpool.FindUserByName(props.Username)
		if err != nil {
			logger.Error("could not find user", "err", err.Error())
			w.WriteHeader(http.StatusNotFound)
			return
		}

		logger = logger.With(
			"user", u.Name,
			"project", props.ProjectName,
		)
		p, err := dbpool.FindProjectByName(u.ID, props.ProjectName)
		if err != nil {
			logger.Error(
				"could not find project for user",
				"user", u.Name,
				"project", props.ProjectName,
				"err", err.Error(),
			)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if u != nil && p != nil {
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	w.WriteHeader(http.StatusNotFound)
}

// CacheMgmt applies the purges published on the cache-drain pipe. onPurge,
// when set, clears whatever else is cached about the site; it runs first so
// the http cache isn't refilled from stale records.
func CacheMgmt(ctx context.Context, cfg *PgsConfig, cache *SiteCache, onPurge func(site string)) {
	cfg.Logger.Info("cache mgmt initiated")
	for {
		scanner := bufio.NewScanner(cfg.Pubsub)
		scanner.Buffer(make([]byte, 32*1024), 32*1024)
		for scanner.Scan() {
			subdomain := strings.TrimSpace(scanner.Text())
			cfg.Logger.Info("received cache-drain item", "subdomain", subdomain)
			if onPurge != nil {
				onPurge(subdomain)
			}

			if subdomain == "*" {
				cache.Purge()
				cfg.Logger.Info("successfully cleared cache from remote cli request")
				continue
			}
			cache.PurgeSite(subdomain)
		}
	}
}

func (web *WebRouter) createRssHandler(by string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dbpool := web.Cfg.DB
		logger := web.Cfg.Logger
		cfg := web.Cfg

		projects, err := dbpool.FindProjects(by)
		if err != nil {
			logger.Error("could not find projects", "err", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		feed := &feeds.Feed{
			Title:       fmt.Sprintf("%s discovery feed %s", cfg.Domain, by),
			Link:        &feeds.Link{Href: "https://pgs.sh"},
			Description: fmt.Sprintf("%s projects %s", cfg.Domain, by),
			Author:      &feeds.Author{Name: cfg.Domain},
			Created:     time.Now(),
		}

		var feedItems []*feeds.Item
		for _, project := range projects {
			realUrl := strings.TrimSuffix(
				cfg.AssetURL(project.Username, project.Name, ""),
				"/",
			)
			uat := project.UpdatedAt.Unix()
			id := realUrl
			title := fmt.Sprintf("%s-%s", project.Username, project.Name)
			if by == "updated_at" {
				id = fmt.Sprintf("%s:%d", realUrl, uat)
				title = fmt.Sprintf("%s - %d", title, uat)
			}

			item := &feeds.Item{
				Id:          id,
				Title:       title,
				Link:        &feeds.Link{Href: realUrl},
				Content:     fmt.Sprintf(`<a href="%s">%s</a>`, realUrl, realUrl),
				Created:     *project.CreatedAt,
				Updated:     *project.CreatedAt,
				Description: "",
				Author:      &feeds.Author{Name: project.Username},
			}

			feedItems = append(feedItems, item)
		}
		feed.Items = feedItems

		rss, err := feed.ToAtom()
		if err != nil {
			logger.Error("could not convert feed to atom", "err", err.Error())
			http.Error(w, "Could not generate atom rss feed", http.StatusInternalServerError)
		}

		w.Header().Add("Content-Type", "application/atom+xml")
		_, err = w.Write([]byte(rss))
		if err != nil {
			logger.Error("http write failed", "err", err.Error())
		}
	}
}

func WebPerm(proj *db.Project) bool {
	return proj.Acl.Type == "public" || proj.Acl.Type == ""
}

var imgRegex = regexp.MustCompile(`(.+\.(?:jpg|jpeg|png|gif|webp|svg))(/.+)`)

func (web *WebRouter) AssetRequest(perm func(proj *db.Project) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fname := r.PathValue("fname")
		if imgRegex.MatchString(fname) {
			web.ImageRequest(perm)(w, r)
			return
		}
		web.ServeAsset(fname, nil, perm, w, r)
	}
}

func (web *WebRouter) ImageRequest(perm func(proj *db.Project) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rawname := r.PathValue("fname")
		matches := imgRegex.FindStringSubmatch(rawname)
		fname := rawname
		imgOpts := ""
		if len(matches) >= 2 {
			fname = matches[1]
		}
		if len(matches) >= 3 {
			imgOpts = matches[2]
		}

		opts, err := storage.UriToImgProcessOpts(imgOpts)
		if err != nil {
			errMsg := fmt.Sprintf("ERROR: error processing img options: %s", err.Error())
			web.Cfg.Logger.Error("ERROR: processing img options", "err", errMsg)
			http.Error(w, errMsg, http.StatusUnprocessableEntity)
			return
		}

		web.ServeAsset(fname, opts, perm, w, r)
	}
}

func (web *WebRouter) ServeAsset(fname string, opts *storage.ImgProcessOpts, hasPerm HasPerm, w http.ResponseWriter, r *http.Request) {
	subdomain := router.GetSubdomain(r)

	logger := web.Cfg.Logger.With(
		"subdomain", subdomain,
		"filename", fname,
		"url", fmt.Sprintf("%s%s", r.Host, r.URL.Path),
		"host", r.Host,
	)

	props, err := router.GetProjectFromSubdomain(subdomain)
	if err != nil {
		logger.Info(
			"could not determine project from subdomain",
			"err", err,
		)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	logger = logger.With(
		"project", props.ProjectName,
		"user", props.Username,
	)

	site, status, msg := web.findSite(props, logger)
	if site == nil {
		http.Error(w, msg, status)
		return
	}
	user, project, bucket := site.user, site.project, site.bucket

	logger = logger.With(
		"userId", user.ID,
		"projectId", project.ID,
		"project", project.Name,
	)

	if project.Blocked != "" {
		logger.Error("project has been blocked")
		http.Error(w, project.Blocked, http.StatusForbidden)
		return
	}

	if project.Acl.Type == "http-pass" {
		cookie, err := r.Cookie(getCookieName(project.Name))
		if err == nil {
			if cookie.Valid() != nil || cookie.Value != project.ID {
				logger.Error("cookie not valid", "err", err)
				web.serveLoginForm(w, r, project, logger)
				return
			}
		} else {
			if errors.Is(err, http.ErrNoCookie) {
				web.serveLoginForm(w, r, project, logger)
				return
			} else {
				// Some other error occurred
				logger.Error("failed to fetch cookie", "err", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	} else if !hasPerm(project) {
		logger.Error("You do not have access to this site")
		http.Error(w, "You do not have access to this site", http.StatusUnauthorized)
		return
	}

	asset := &ApiAssetHandler{
		WebRouter: web,
		Logger:    logger,

		Username:       props.Username,
		UserID:         user.ID,
		Subdomain:      subdomain,
		ProjectID:      project.ID,
		ProjectDir:     project.ProjectDir,
		Filepath:       fname,
		Bucket:         bucket,
		ImgProcessOpts: opts,
		HasPicoPlus:    site.hasPicoPlus(),
		HttpPass:       project.Acl.Type == "http-pass",
	}

	asset.ServeHTTP(w, r)
}

func (web *WebRouter) serveLoginForm(w http.ResponseWriter, r *http.Request, project *db.Project, logger *slog.Logger) {
	serveLoginFormWithConfig(w, r, project, web.Cfg, logger)
}

func (web *WebRouter) handleLogin(w http.ResponseWriter, r *http.Request) {
	handleLogin(w, r, web.Cfg)
}

func (web *WebRouter) handleAutoForm(w http.ResponseWriter, r *http.Request) {
	handleAutoForm(w, r, web.Cfg)
}
