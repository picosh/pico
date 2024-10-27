package pgs

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"net/http/httputil"
	_ "net/http/pprof"

	"github.com/gorilla/feeds"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	sst "github.com/picosh/pobj/storage"
)

type AssetHandler struct {
	*WebRouter
	Logger         *slog.Logger
	Username       string
	Subdomain      string
	Filepath       string
	ProjectDir     string
	UserID         string
	Bucket         sst.Bucket
	ImgProcessOpts *storage.ImgProcessOpts
	ProjectID      string
	HasPicoPlus    bool
}

func hasProtocol(url string) bool {
	isFullUrl := strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
	return isFullUrl
}

func (h *AssetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger := h.Logger
	var redirects []*RedirectRule
	redirectFp, redirectInfo, err := h.Storage.GetObject(h.Bucket, filepath.Join(h.ProjectDir, "_redirects"))
	if err == nil {
		defer redirectFp.Close()
		if redirectInfo != nil && redirectInfo.Size > h.Cfg.MaxSpecialFileSize {
			errMsg := fmt.Sprintf("_redirects file is too large (%d > %d)", redirectInfo.Size, h.Cfg.MaxSpecialFileSize)
			logger.Error(errMsg)
			http.Error(w, errMsg, http.StatusInternalServerError)
			return
		}
		buf := new(strings.Builder)
		lr := io.LimitReader(redirectFp, h.Cfg.MaxSpecialFileSize)
		_, err := io.Copy(buf, lr)
		if err != nil {
			logger.Error("io copy", "err", err.Error())
			http.Error(w, "cannot read _redirects file", http.StatusInternalServerError)
			return
		}

		redirects, err = parseRedirectText(buf.String())
		if err != nil {
			logger.Error("could not parse redirect text", "err", err.Error())
		}
	}

	routes := calcRoutes(h.ProjectDir, h.Filepath, redirects)

	var contents io.ReadCloser
	contentType := ""
	assetFilepath := ""
	info := &sst.ObjectInfo{}
	status := http.StatusOK
	attempts := []string{}
	for _, fp := range routes {
		if checkIsRedirect(fp.Status) {
			// hack: check to see if there's an index file in the requested directory
			// before redirecting, this saves a hop that will just end up a 404
			if !hasProtocol(fp.Filepath) && strings.HasSuffix(fp.Filepath, "/") {
				next := filepath.Join(h.ProjectDir, fp.Filepath, "index.html")
				_, _, err := h.Storage.GetObject(h.Bucket, next)
				if err != nil {
					continue
				}
			}
			logger.Info(
				"redirecting request",
				"destination", fp.Filepath,
				"status", fp.Status,
			)
			http.Redirect(w, r, fp.Filepath, fp.Status)
			return
		} else if hasProtocol(fp.Filepath) {
			if !h.HasPicoPlus {
				msg := "must be pico+ user to fetch content from external source"
				logger.Error(
					msg,
					"destination", fp.Filepath,
					"status", fp.Status,
				)
				http.Error(w, msg, http.StatusUnauthorized)
				return
			}

			logger.Info(
				"fetching content from external service",
				"destination", fp.Filepath,
				"status", fp.Status,
			)

			destUrl, err := url.Parse(fp.Filepath)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			proxy := httputil.NewSingleHostReverseProxy(destUrl)
			oldDirector := proxy.Director
			proxy.Director = func(r *http.Request) {
				oldDirector(r)
				r.Host = destUrl.Host
				r.URL = destUrl
			}
			proxy.ServeHTTP(w, r)
			return
		}

		attempts = append(attempts, fp.Filepath)
		mimeType := storage.GetMimeType(fp.Filepath)
		logger = logger.With("filename", fp.Filepath)
		var c io.ReadCloser
		var err error
		if strings.HasPrefix(mimeType, "image/") {
			c, contentType, err = h.Storage.ServeObject(
				h.Bucket,
				fp.Filepath,
				h.ImgProcessOpts,
			)
		} else {
			c, info, err = h.Storage.GetObject(h.Bucket, fp.Filepath)
		}
		if err == nil {
			contents = c
			assetFilepath = fp.Filepath
			status = fp.Status
			break
		}
	}

	if assetFilepath == "" {
		logger.Info(
			"asset not found in bucket",
			"routes", strings.Join(attempts, ", "),
			"status", http.StatusNotFound,
		)
		// track 404s
		ch := h.AnalyticsQueue
		view, err := shared.AnalyticsVisitFromRequest(r, h.Dbpool, h.UserID, h.Cfg.Secret)
		if err == nil {
			view.ProjectID = h.ProjectID
			view.Status = http.StatusNotFound
			ch <- view
		} else {
			if !errors.Is(err, shared.ErrAnalyticsDisabled) {
				logger.Error("could not record analytics view", "err", err)
			}
		}
		http.Error(w, "404 not found", http.StatusNotFound)
		return
	}
	defer contents.Close()

	if contentType == "" {
		contentType = storage.GetMimeType(assetFilepath)
	}

	var headers []*HeaderRule
	headersFp, headersInfo, err := h.Storage.GetObject(h.Bucket, filepath.Join(h.ProjectDir, "_headers"))
	if err == nil {
		defer headersFp.Close()
		if headersInfo != nil && headersInfo.Size > h.Cfg.MaxSpecialFileSize {
			errMsg := fmt.Sprintf("_headers file is too large (%d > %d)", headersInfo.Size, h.Cfg.MaxSpecialFileSize)
			logger.Error(errMsg)
			http.Error(w, errMsg, http.StatusInternalServerError)
			return
		}
		buf := new(strings.Builder)
		lr := io.LimitReader(headersFp, h.Cfg.MaxSpecialFileSize)
		_, err := io.Copy(buf, lr)
		if err != nil {
			logger.Error("io copy", "err", err.Error())
			http.Error(w, "cannot read _headers file", http.StatusInternalServerError)
			return
		}

		headers, err = parseHeaderText(buf.String())
		if err != nil {
			logger.Error("could not parse header text", "err", err.Error())
		}
	}

	userHeaders := []*HeaderLine{}
	for _, headerRule := range headers {
		rr := regexp.MustCompile(headerRule.Path)
		match := rr.FindStringSubmatch(assetFilepath)
		if len(match) > 0 {
			userHeaders = headerRule.Headers
		}
	}

	if info != nil {
		if info.ETag != "" {
			w.Header().Add("etag", info.ETag)
		}

		if !info.LastModified.IsZero() {
			w.Header().Add("last-modified", info.LastModified.Format(http.TimeFormat))
		}
	}

	for _, hdr := range userHeaders {
		w.Header().Add(hdr.Name, hdr.Value)
	}
	if w.Header().Get("content-type") == "" {
		w.Header().Set("content-type", contentType)
	}

	finContentType := w.Header().Get("content-type")

	// only track pages, not individual assets
	if finContentType == "text/html" {
		// track visit
		ch := h.AnalyticsQueue
		view, err := shared.AnalyticsVisitFromRequest(r, h.Dbpool, h.UserID, h.Cfg.Secret)
		if err == nil {
			view.ProjectID = h.ProjectID
			ch <- view
		} else {
			if !errors.Is(err, shared.ErrAnalyticsDisabled) {
				logger.Error("could not record analytics view", "err", err)
			}
		}
	}

	logger.Info(
		"serving asset",
		"asset", assetFilepath,
		"status", status,
		"contentType", finContentType,
	)

	w.WriteHeader(status)
	_, err = io.Copy(w, contents)

	if err != nil {
		logger.Error("io copy", "err", err.Error())
	}
}

type SubdomainProps struct {
	ProjectName string
	Username    string
}

func getProjectFromSubdomain(subdomain string) (*SubdomainProps, error) {
	props := &SubdomainProps{}
	strs := strings.SplitN(subdomain, "-", 2)
	props.Username = strs[0]

	if len(strs) == 2 {
		props.ProjectName = strs[1]
	} else {
		props.ProjectName = props.Username
	}

	return props, nil
}

type HasPerm = func(proj *db.Project) bool

type WebRouter struct {
	Cfg            *shared.ConfigSite
	Logger         *slog.Logger
	Dbpool         db.DB
	Storage        storage.StorageServe
	AnalyticsQueue chan *db.AnalyticsVisits
}

func NewWebRouter(cfg *shared.ConfigSite, logger *slog.Logger, dbpool db.DB, st storage.StorageServe, analytics chan *db.AnalyticsVisits) *WebRouter {
	return &WebRouter{
		Cfg:            cfg,
		Logger:         logger,
		Dbpool:         dbpool,
		Storage:        st,
		AnalyticsQueue: analytics,
	}
}

func (web *WebRouter) checkHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := web.Dbpool
	cfg := web.Cfg
	logger := web.Logger

	if cfg.IsCustomdomains() {
		hostDomain := r.URL.Query().Get("domain")
		appDomain := strings.Split(cfg.Domain, ":")[0]

		if !strings.Contains(hostDomain, appDomain) {
			subdomain := shared.GetCustomDomain(hostDomain, cfg.Space)
			props, err := getProjectFromSubdomain(subdomain)
			if err != nil {
				logger.Error(
					"could not get project from subdomain",
					"subdomain", subdomain,
					"err", err.Error(),
				)
				w.WriteHeader(http.StatusNotFound)
				return
			}

			u, err := dbpool.FindUserForName(props.Username)
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
	}

	w.WriteHeader(http.StatusNotFound)
}

type RssData struct {
	Contents template.HTML
}

func (web *WebRouter) createRssHandler(by string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dbpool := web.Dbpool
		logger := web.Logger
		cfg := web.Cfg

		pager, err := dbpool.FindAllProjects(&db.Pager{Num: 100, Page: 0}, by)
		if err != nil {
			logger.Error("could not find projects", "err", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		feed := &feeds.Feed{
			Title:       fmt.Sprintf("%s discovery feed %s", cfg.Domain, by),
			Link:        &feeds.Link{Href: cfg.ReadURL()},
			Description: fmt.Sprintf("%s projects %s", cfg.Domain, by),
			Author:      &feeds.Author{Name: cfg.Domain},
			Created:     time.Now(),
		}

		var feedItems []*feeds.Item
		for _, project := range pager.Data {
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

func (web *WebRouter) Perm(proj *db.Project) bool {
	return proj.Acl.Type == "public"
}

func (web *WebRouter) AssetRequest(w http.ResponseWriter, r *http.Request) {
	fname := r.PathValue("fname")
	web.ServeAsset(fname, nil, false, web.Perm, w, r)
}

func (web *WebRouter) ImageRequest(w http.ResponseWriter, r *http.Request) {
	fname := r.PathValue("fname")
	imgOpts := r.PathValue("options")
	opts, err := storage.UriToImgProcessOpts(imgOpts)
	if err != nil {
		errMsg := fmt.Sprintf("error processing img options: %s", err.Error())
		web.Logger.Error("error processing img options", "err", errMsg)
		http.Error(w, errMsg, http.StatusUnprocessableEntity)
		return
	}

	web.ServeAsset(fname, opts, false, web.Perm, w, r)
}

func (web *WebRouter) ServeAsset(fname string, opts *storage.ImgProcessOpts, fromImgs bool, hasPerm HasPerm, w http.ResponseWriter, r *http.Request) {
	subdomain := shared.GetSubdomain(r)

	logger := web.Logger.With(
		"subdomain", subdomain,
		"filename", fname,
		"url", fmt.Sprintf("%s%s", r.Host, r.URL.Path),
		"host", r.Host,
	)

	props, err := getProjectFromSubdomain(subdomain)
	if err != nil {
		logger.Info(
			"could parse project from subdomain",
			"err", err,
		)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	logger = logger.With(
		"project", props.ProjectName,
		"user", props.Username,
	)

	user, err := web.Dbpool.FindUserForName(props.Username)
	if err != nil {
		logger.Info("user not found")
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	logger = logger.With(
		"userId", user.ID,
	)

	projectID := ""
	// TODO: this could probably be cleaned up more
	// imgs wont have a project directory
	projectDir := ""
	var bucket sst.Bucket
	// imgs has a different bucket directory
	if fromImgs {
		bucket, err = web.Storage.GetBucket(shared.GetImgsBucketName(user.ID))
	} else {
		bucket, err = web.Storage.GetBucket(shared.GetAssetBucketName(user.ID))
		project, err := web.Dbpool.FindProjectByName(user.ID, props.ProjectName)
		if err != nil {
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

		projectID = project.ID
		projectDir = project.ProjectDir
		if !hasPerm(project) {
			http.Error(w, "You do not have access to this site", http.StatusUnauthorized)
			return
		}
	}

	if err != nil {
		logger.Info("bucket not found")
		http.Error(w, "bucket not found", http.StatusNotFound)
		return
	}

	hasPicoPlus := web.Dbpool.HasFeatureForUser(user.ID, "plus")

	asset := &AssetHandler{
		WebRouter: web,
		Logger:    logger,

		Username:       props.Username,
		UserID:         user.ID,
		Subdomain:      subdomain,
		ProjectDir:     projectDir,
		Filepath:       fname,
		Bucket:         bucket,
		ImgProcessOpts: opts,
		ProjectID:      projectID,
		HasPicoPlus:    hasPicoPlus,
	}

	asset.ServeHTTP(w, r)
}

func (web *WebRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	router := http.NewServeMux()
	subdomain := shared.GetSubdomainFromRequest(r, web.Cfg.Domain, web.Cfg.Space)

	if subdomain == "" {
		// root routes
		router.HandleFunc("GET /check", web.checkHandler)
		router.Handle("GET /main.css", shared.ServeFile("main.css", "text/css"))
		router.Handle("GET /card.png", shared.ServeFile("card.png", "image/png"))
		router.Handle("GET /favicon-16x16.png", shared.ServeFile("favicon-16x16.png", "image/png"))
		router.Handle("GET /apple-touch-icon.png", shared.ServeFile("apple-touch-icon.png", "image/png"))
		router.Handle("GET /favicon.ico", shared.ServeFile("favicon.ico", "image/x-icon"))
		router.Handle("GET /robots.txt", shared.ServeFile("robots.txt", "text/plain"))
		router.Handle("GET /rss/updated", web.createRssHandler("updated_at"))
		router.Handle("GET /rss", web.createRssHandler("created_at"))
		router.Handle("GET /{$}", shared.CreatePageHandler("html/marketing.page.tmpl"))
	} else {
		// user routes
		router.HandleFunc("GET /{fname}/{options...}", web.ImageRequest)
		router.HandleFunc("GET /{fname}", web.AssetRequest)
		router.HandleFunc("GET /{$}", web.AssetRequest)
	}

	ctx := r.Context()
	ctx = context.WithValue(ctx, shared.CtxSubdomainKey{}, subdomain)
	router.ServeHTTP(w, r.WithContext(ctx))
}

func StartApiServer() {
	cfg := NewConfigSite()
	logger := cfg.Logger

	dbpool := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbpool.Close()

	var st storage.StorageServe
	var err error
	if cfg.MinioURL == "" {
		st, err = storage.NewStorageFS(cfg.StorageDir)
	} else {
		st, err = storage.NewStorageMinio(cfg.MinioURL, cfg.MinioUser, cfg.MinioPass)
	}

	if err != nil {
		logger.Error("could not connect to object storage", "err", err.Error())
		return
	}

	ch := make(chan *db.AnalyticsVisits)
	go shared.AnalyticsCollect(ch, dbpool, logger)

	routes := NewWebRouter(cfg, logger, dbpool, st, ch)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Info(
		"starting server on port",
		"port", cfg.Port,
		"domain", cfg.Domain,
	)
	err = http.ListenAndServe(portStr, routes)
	logger.Error(
		"listen and serve",
		"err", err.Error(),
	)
}
