package pgs

import (
	"bufio"
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "net/http/pprof"

	"github.com/darkweak/souin/configurationtypes"
	"github.com/darkweak/souin/pkg/middleware"
	"github.com/darkweak/souin/plugins/souin/storages"
	"github.com/darkweak/storages/core"
	"github.com/gorilla/feeds"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/picosh/pico/pkg/cache"
	"github.com/picosh/pico/pkg/db"
	sst "github.com/picosh/pico/pkg/pobj/storage"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/storage"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/protobuf/proto"
)

type CachedHttp struct {
	handler *middleware.SouinBaseHandler
	routes  *WebRouter
}

func (c *CachedHttp) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	_ = c.handler.ServeHTTP(writer, req, func(w http.ResponseWriter, r *http.Request) error {
		c.routes.ServeHTTP(w, r)
		return nil
	})
}

func SetupCache(cfg *PgsConfig) *middleware.SouinBaseHandler {
	ttl := configurationtypes.Duration{Duration: cfg.CacheTTL}
	stale := configurationtypes.Duration{Duration: cfg.CacheTTL * 2}
	c := &middleware.BaseConfiguration{
		API: configurationtypes.API{
			Prometheus: configurationtypes.APIEndpoint{
				Enable: true,
			},
		},
		DefaultCache: &configurationtypes.DefaultCache{
			TTL:   ttl,
			Stale: stale,
			Otter: configurationtypes.CacheProvider{
				Uuid:          fmt.Sprintf("OTTER-%s", stale),
				Configuration: map[string]interface{}{},
			},
			Regex: configurationtypes.Regex{
				Exclude: "/check|/_metrics",
			},
			MaxBodyBytes:        uint64(cfg.MaxAssetSize),
			DefaultCacheControl: cfg.CacheControl,
		},
	}
	c.SetLogger(&CompatLogger{Logger: cfg.Logger})
	storages.InitFromConfiguration(c)
	return middleware.NewHTTPCacheHandler(c)
}

func StartApiServer(cfg *PgsConfig) {
	ctx := context.Background()

	httpCache := SetupCache(cfg)
	routes := NewWebRouter(cfg)
	cacher := &CachedHttp{
		handler: httpCache,
		routes:  routes,
	}

	go routes.CacheMgmt(ctx, httpCache, cfg.CacheClearingQueue)

	portStr := fmt.Sprintf(":%s", cfg.WebPort)
	cfg.Logger.Info(
		"starting server on port",
		"port", cfg.WebPort,
		"domain", cfg.Domain,
	)
	err := http.ListenAndServe(portStr, cacher)
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
	HeadersCache   *expirable.LRU[string, []*HeaderRule]
}

func NewWebRouter(cfg *PgsConfig) *WebRouter {
	router := newWebRouter(cfg)
	go router.WatchCacheClear()
	return router
}

func newWebRouter(cfg *PgsConfig) *WebRouter {
	router := &WebRouter{
		Cfg:            cfg,
		RedirectsCache: expirable.NewLRU[string, []*RedirectRule](2048, nil, cache.CacheTimeout),
		HeadersCache:   expirable.NewLRU[string, []*HeaderRule](2048, nil, cache.CacheTimeout),
	}
	router.initRouters()
	return router
}

func (web *WebRouter) WatchCacheClear() {
	for key := range web.Cfg.CacheClearingQueue {
		web.Cfg.Logger.Info("lru cache clear request", "key", key)
		rKey := filepath.Join(key, "_redirects")
		web.RedirectsCache.Remove(rKey)
		hKey := filepath.Join(key, "_headers")
		web.HeadersCache.Remove(hKey)
	}
}

func (web *WebRouter) initRouters() {
	// ensure legacy router is disabled
	// GODEBUG=httpmuxgo121=0

	// root domain
	rootRouter := http.NewServeMux()
	rootRouter.HandleFunc("GET /check", web.checkHandler)
	rootRouter.HandleFunc("GET /_metrics", promhttp.Handler().ServeHTTP)
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
	appDomain := strings.Split(cfg.Domain, ":")[0]

	if !strings.Contains(hostDomain, appDomain) {
		subdomain := shared.GetCustomDomain(hostDomain, cfg.TxtPrefix)
		props, err := shared.GetProjectFromSubdomain(subdomain)
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

func (web *WebRouter) CacheMgmt(ctx context.Context, httpCache *middleware.SouinBaseHandler, notify chan string) {
	storer := httpCache.Storers[0]

	for {
		scanner := bufio.NewScanner(web.Cfg.Pubsub)
		scanner.Buffer(make([]byte, 32*1024), 32*1024)
		for scanner.Scan() {
			surrogateKey := strings.TrimSpace(scanner.Text())
			web.Cfg.Logger.Info("received cache-drain item", "surrogateKey", surrogateKey)
			notify <- surrogateKey

			if surrogateKey == "*" {
				storer.DeleteMany(".+")
				err := httpCache.SurrogateKeyStorer.Destruct()
				if err != nil {
					web.Cfg.Logger.Error("could not clear cache and surrogate key store", "err", err)
				} else {
					web.Cfg.Logger.Info("successfully cleared cache and surrogate keys store")
				}
				continue
			}

			var header http.Header = map[string][]string{}
			header.Add("Surrogate-Key", surrogateKey)

			ck, _ := httpCache.SurrogateKeyStorer.Purge(header)
			for _, key := range ck {
				key, _ = strings.CutPrefix(key, core.MappingKeyPrefix)
				if b := storer.Get(core.MappingKeyPrefix + key); len(b) > 0 {
					var mapping core.StorageMapper
					if e := proto.Unmarshal(b, &mapping); e == nil {
						for k := range mapping.GetMapping() {
							qkey, _ := url.QueryUnescape(k)
							web.Cfg.Logger.Info(
								"deleting key from surrogate cache",
								"surrogateKey", surrogateKey,
								"key", qkey,
							)
							storer.Delete(qkey)
						}
					}
				}

				qkey, _ := url.QueryUnescape(key)
				web.Cfg.Logger.Info(
					"deleting from cache",
					"surrogateKey", surrogateKey,
					"key", core.MappingKeyPrefix+qkey,
				)
				storer.Delete(core.MappingKeyPrefix + qkey)
			}
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
			errMsg := fmt.Sprintf("error processing img options: %s", err.Error())
			web.Cfg.Logger.Error("error processing img options", "err", errMsg)
			http.Error(w, errMsg, http.StatusUnprocessableEntity)
			return
		}

		web.ServeAsset(fname, opts, perm, w, r)
	}
}

func (web *WebRouter) ServeAsset(fname string, opts *storage.ImgProcessOpts, hasPerm HasPerm, w http.ResponseWriter, r *http.Request) {
	subdomain := shared.GetSubdomain(r)

	logger := web.Cfg.Logger.With(
		"subdomain", subdomain,
		"filename", fname,
		"url", fmt.Sprintf("%s%s", r.Host, r.URL.Path),
		"host", r.Host,
	)

	props, err := shared.GetProjectFromSubdomain(subdomain)
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

	user, err := web.Cfg.DB.FindUserByName(props.Username)
	if err != nil {
		logger.Info("user not found")
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	logger = logger.With(
		"userId", user.ID,
	)

	var bucket sst.Bucket
	bucket, err = web.Cfg.Storage.GetBucket(shared.GetAssetBucketName(user.ID))
	project, perr := web.Cfg.DB.FindProjectByName(user.ID, props.ProjectName)
	if perr != nil {
		logger.Info("project not found")
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	logger = logger.With(
		"projectId", project.ID,
		"project", project.Name,
	)

	if project.Blocked != "" {
		logger.Error("project has been blocked")
		http.Error(w, project.Blocked, http.StatusForbidden)
		return
	}

	if !hasPerm(project) {
		logger.Error("You do not have access to this site")
		http.Error(w, "You do not have access to this site", http.StatusUnauthorized)
		return
	}

	if err != nil {
		logger.Error("bucket not found", "err", err)
		http.Error(w, "bucket not found", http.StatusNotFound)
		return
	}

	hasPicoPlus := false
	ff, _ := web.Cfg.DB.FindFeature(user.ID, "plus")
	if ff != nil {
		if ff.ExpiresAt.Before(time.Now()) {
			hasPicoPlus = true
		}
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
		HasPicoPlus:    hasPicoPlus,
	}

	asset.ServeHTTP(w, r)
}

func (web *WebRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	subdomain := shared.GetSubdomainFromRequest(r, web.Cfg.Domain, web.Cfg.TxtPrefix)
	if web.RootRouter == nil || web.UserRouter == nil {
		web.Cfg.Logger.Error("routers not initialized")
		http.Error(w, "routers not initialized", http.StatusInternalServerError)
		return
	}

	var router *http.ServeMux
	if subdomain == "" {
		router = web.RootRouter
	} else {
		router = web.UserRouter
	}

	ctx := r.Context()
	ctx = context.WithValue(ctx, shared.CtxSubdomainKey{}, subdomain)
	router.ServeHTTP(w, r.WithContext(ctx))
}

type CompatLogger struct {
	Logger *slog.Logger
}

func (cl *CompatLogger) marshall(int ...interface{}) string {
	res := ""
	for _, val := range int {
		switch r := val.(type) {
		case string:
			res += " " + r
		}
	}
	return res
}
func (cl *CompatLogger) DPanic(int ...interface{}) {
	cl.Logger.Error("panic", "output", cl.marshall(int))
}
func (cl *CompatLogger) DPanicf(st string, int ...interface{}) {
	cl.Logger.Error(fmt.Sprintf(st, int...))
}
func (cl *CompatLogger) Debug(int ...interface{}) {
	cl.Logger.Debug("debug", "output", cl.marshall(int))
}
func (cl *CompatLogger) Debugf(st string, int ...interface{}) {
	cl.Logger.Debug(fmt.Sprintf(st, int...))
}
func (cl *CompatLogger) Error(int ...interface{}) {
	cl.Logger.Error("error", "output", cl.marshall(int))
}
func (cl *CompatLogger) Errorf(st string, int ...interface{}) {
	cl.Logger.Error(fmt.Sprintf(st, int...))
}
func (cl *CompatLogger) Fatal(int ...interface{}) {
	cl.Logger.Error("fatal", "outpu", cl.marshall(int))
}
func (cl *CompatLogger) Fatalf(st string, int ...interface{}) {
	cl.Logger.Error(fmt.Sprintf(st, int...))
}
func (cl *CompatLogger) Info(int ...interface{}) {
	cl.Logger.Info("info", "output", cl.marshall(int))
}
func (cl *CompatLogger) Infof(st string, int ...interface{}) {
	cl.Logger.Info(fmt.Sprintf(st, int...))
}
func (cl *CompatLogger) Panic(int ...interface{}) {
	cl.Logger.Error("panic", "output", cl.marshall(int))
}
func (cl *CompatLogger) Panicf(st string, int ...interface{}) {
	cl.Logger.Error(fmt.Sprintf(st, int...))
}
func (cl *CompatLogger) Warn(int ...interface{}) {
	cl.Logger.Warn("warn", "output", cl.marshall(int))
}
func (cl *CompatLogger) Warnf(st string, int ...interface{}) {
	cl.Logger.Warn(fmt.Sprintf(st, int...))
}
