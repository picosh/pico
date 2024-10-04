package pgs

import (
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

	_ "net/http/pprof"

	"github.com/gorilla/feeds"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	sst "github.com/picosh/pobj/storage"
)

type AssetHandler struct {
	Username       string
	Subdomain      string
	Filepath       string
	ProjectDir     string
	Cfg            *shared.ConfigSite
	Dbpool         db.DB
	Storage        storage.StorageServe
	Logger         *slog.Logger
	UserID         string
	Bucket         sst.Bucket
	ImgProcessOpts *storage.ImgProcessOpts
	ProjectID      string
}

func checkHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := shared.GetDB(r)
	cfg := shared.GetCfg(r)
	logger := shared.GetLogger(r)

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

func createRssHandler(by string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dbpool := shared.GetDB(r)
		logger := shared.GetLogger(r)
		cfg := shared.GetCfg(r)

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

func hasProtocol(url string) bool {
	isFullUrl := strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
	return isFullUrl
}

func (h *AssetHandler) handle(logger *slog.Logger, w http.ResponseWriter, r *http.Request) {
	var redirects []*RedirectRule
	redirectFp, _, _, err := h.Storage.GetObject(h.Bucket, filepath.Join(h.ProjectDir, "_redirects"))
	if err == nil {
		defer redirectFp.Close()
		buf := new(strings.Builder)
		_, err := io.Copy(buf, redirectFp)
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
	status := http.StatusOK
	attempts := []string{}
	for _, fp := range routes {
		if checkIsRedirect(fp.Status) {
			// hack: check to see if there's an index file in the requested directory
			// before redirecting, this saves a hop that will just end up a 404
			if !hasProtocol(fp.Filepath) && strings.HasSuffix(fp.Filepath, "/") {
				next := filepath.Join(h.ProjectDir, fp.Filepath, "index.html")
				_, _, _, err := h.Storage.GetObject(h.Bucket, next)
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
			logger.Info(
				"fetching content from external service",
				"destination", fp.Filepath,
				"status", fp.Status,
			)
			// fetch content from url and serve it
			resp, err := http.Get(fp.Filepath)
			if err != nil {
				logger.Error(
					"external service not found",
					"err", err,
					"status", http.StatusNotFound,
				)
				http.Error(w, "404 not found", http.StatusNotFound)
				return
			}

			w.Header().Set("content-type", resp.Header.Get("content-type"))
			w.WriteHeader(status)
			_, err = io.Copy(w, resp.Body)
			if err != nil {
				logger.Error("io copy", "err", err.Error())
			}
			return
		}

		attempts = append(attempts, fp.Filepath)
		mimeType := storage.GetMimeType(fp.Filepath)
		var c io.ReadCloser
		var err error
		if strings.HasPrefix(mimeType, "image/") {
			c, contentType, err = h.Storage.ServeObject(
				h.Bucket,
				fp.Filepath,
				h.ImgProcessOpts,
			)
		} else {
			c, _, _, err = h.Storage.GetObject(h.Bucket, fp.Filepath)
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
		ch := shared.GetAnalyticsQueue(r)
		view, err := shared.AnalyticsVisitFromRequest(r, h.UserID, h.Cfg.Secret)
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
	headersFp, _, _, err := h.Storage.GetObject(h.Bucket, filepath.Join(h.ProjectDir, "_headers"))
	if err == nil {
		defer headersFp.Close()
		buf := new(strings.Builder)
		_, err := io.Copy(buf, headersFp)
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
		ch := shared.GetAnalyticsQueue(r)
		view, err := shared.AnalyticsVisitFromRequest(r, h.UserID, h.Cfg.Secret)
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

func ServeAsset(fname string, opts *storage.ImgProcessOpts, fromImgs bool, hasPerm HasPerm, w http.ResponseWriter, r *http.Request) {
	subdomain := shared.GetSubdomain(r)
	cfg := shared.GetCfg(r)
	dbpool := shared.GetDB(r)
	st := shared.GetStorage(r)
	ologger := shared.GetLogger(r)

	logger := ologger.With(
		"subdomain", subdomain,
		"filename", fname,
		"url", r.URL.Path,
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

	user, err := dbpool.FindUserForName(props.Username)
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
		bucket, err = st.GetBucket(shared.GetImgsBucketName(user.ID))
	} else {
		bucket, err = st.GetBucket(shared.GetAssetBucketName(user.ID))
		project, err := dbpool.FindProjectByName(user.ID, props.ProjectName)
		if err != nil {
			logger.Info("project not found")
			http.Error(w, "project not found", http.StatusNotFound)
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

	asset := &AssetHandler{
		Username:       props.Username,
		UserID:         user.ID,
		Subdomain:      subdomain,
		ProjectDir:     projectDir,
		Filepath:       fname,
		Cfg:            cfg,
		Dbpool:         dbpool,
		Storage:        st,
		Logger:         logger,
		Bucket:         bucket,
		ImgProcessOpts: opts,
		ProjectID:      projectID,
	}

	asset.handle(logger, w, r)
}

type HasPerm = func(proj *db.Project) bool

func ImgAssetRequest(hasPerm HasPerm) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := shared.GetLogger(r)
		fname, _ := url.PathUnescape(shared.GetField(r, 0))
		imgOpts, _ := url.PathUnescape(shared.GetField(r, 1))
		opts, err := storage.UriToImgProcessOpts(imgOpts)
		if err != nil {
			errMsg := fmt.Sprintf("error processing img options: %s", err.Error())
			logger.Error("error processing img options", "err", errMsg)
			http.Error(w, errMsg, http.StatusUnprocessableEntity)
		}

		ServeAsset(fname, opts, false, hasPerm, w, r)
	}
}

func AssetRequest(hasPerm HasPerm) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fname, _ := url.PathUnescape(shared.GetField(r, 0))
		ServeAsset(fname, nil, false, hasPerm, w, r)
	}
}

var mainRoutes = []shared.Route{
	shared.NewRoute("GET", "/main.css", shared.ServeFile("main.css", "text/css")),
	shared.NewRoute("GET", "/card.png", shared.ServeFile("card.png", "image/png")),
	shared.NewRoute("GET", "/favicon-16x16.png", shared.ServeFile("favicon-16x16.png", "image/png")),
	shared.NewRoute("GET", "/apple-touch-icon.png", shared.ServeFile("apple-touch-icon.png", "image/png")),
	shared.NewRoute("GET", "/favicon.ico", shared.ServeFile("favicon.ico", "image/x-icon")),
	shared.NewRoute("GET", "/robots.txt", shared.ServeFile("robots.txt", "text/plain")),

	shared.NewRoute("GET", "/", shared.CreatePageHandler("html/marketing.page.tmpl")),
	shared.NewRoute("GET", "/check", checkHandler),
	shared.NewRoute("GET", "/rss/updated", createRssHandler("updated_at")),
	shared.NewRoute("GET", "/rss", createRssHandler("created_at")),
	shared.NewRoute("GET", "/(.+)", shared.CreatePageHandler("html/marketing.page.tmpl")),
}

func createSubdomainRoutes(hasPerm HasPerm) []shared.Route {
	assetRequest := AssetRequest(hasPerm)
	imgRequest := ImgAssetRequest(hasPerm)

	return []shared.Route{
		shared.NewRoute("GET", "/", assetRequest),
		shared.NewRoute("GET", "(/.+.(?:jpg|jpeg|png|gif|webp|svg))(/.+)", imgRequest),
		shared.NewRoute("GET", "(/.+)", assetRequest),
	}
}

func publicPerm(proj *db.Project) bool {
	return proj.Acl.Type == "public"
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
		logger.Error("could not connect to minio", "err", err.Error())
		return
	}

	ch := make(chan *db.AnalyticsVisits)
	go shared.AnalyticsCollect(ch, dbpool, logger)
	apiConfig := &shared.ApiConfig{
		Cfg:            cfg,
		Dbpool:         dbpool,
		Storage:        st,
		AnalyticsQueue: ch,
	}
	handler := shared.CreateServe(mainRoutes, createSubdomainRoutes(publicPerm), apiConfig)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Info(
		"starting server on port",
		"port", cfg.Port,
		"domain", cfg.Domain,
	)
	err = http.ListenAndServe(portStr, router)
	logger.Error(
		"listen and serve",
		"err", err.Error(),
	)
}
