package pgs

import (
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
	gocache "github.com/patrickmn/go-cache"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	sst "github.com/picosh/pobj/storage"
	"github.com/picosh/send/send/utils"
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
	Cache          *gocache.Cache
	UserID         string
	Bucket         sst.Bucket
	ImgProcessOpts *storage.ImgProcessOpts
}

func checkHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := shared.GetDB(r)
	cfg := shared.GetCfg(r)
	logger := shared.GetLogger(r)

	if cfg.IsCustomdomains() {
		hostDomain := r.URL.Query().Get("domain")
		appDomain := strings.Split(cfg.ConfigCms.Domain, ":")[0]

		if !strings.Contains(hostDomain, appDomain) {
			subdomain := shared.GetCustomDomain(hostDomain, cfg.Space)
			props, err := getProjectFromSubdomain(subdomain)
			if err != nil {
				logger.Error(err.Error())
				w.WriteHeader(http.StatusNotFound)
				return
			}

			u, err := dbpool.FindUserForName(props.Username)
			if err != nil {
				logger.Error(err.Error())
				w.WriteHeader(http.StatusNotFound)
				return
			}

			logger = logger.With(
				"user", u.Name,
				"project", props.ProjectName,
			)
			p, err := dbpool.FindProjectByName(u.ID, props.ProjectName)
			if err != nil {
				logger.Error(err.Error())
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

func rssHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	cfg := shared.GetCfg(r)

	pager, err := dbpool.FindAllProjects(&db.Pager{Num: 50, Page: 0})
	if err != nil {
		logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	feed := &feeds.Feed{
		Title:       fmt.Sprintf("%s discovery feed", cfg.Domain),
		Link:        &feeds.Link{Href: cfg.ReadURL()},
		Description: fmt.Sprintf("%s latest projects", cfg.Domain),
		Author:      &feeds.Author{Name: cfg.Domain},
		Created:     time.Now(),
	}

	var feedItems []*feeds.Item
	for _, project := range pager.Data {
		realUrl := strings.TrimSuffix(
			cfg.AssetURL(project.Username, project.Name, ""),
			"/",
		)

		item := &feeds.Item{
			Id:          realUrl,
			Title:       project.Name,
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
		logger.Error(err.Error())
		http.Error(w, "Could not generate atom rss feed", http.StatusInternalServerError)
	}

	w.Header().Add("Content-Type", "application/atom+xml")
	_, err = w.Write([]byte(rss))
	if err != nil {
		logger.Error(err.Error())
	}
}

type HttpReply struct {
	Filepath string
	Query    map[string]string
	Status   int
}

func calcPossibleRoutes(projectName, fp string, userRedirects []*RedirectRule) []*HttpReply {
	fname := filepath.Base(fp)
	fdir := filepath.Dir(fp)
	fext := filepath.Ext(fp)
	mimeType := storage.GetMimeType(fp)
	rts := []*HttpReply{}
	notFound := &HttpReply{
		Filepath: filepath.Join(projectName, "404.html"),
		Status:   404,
	}

	for _, redirect := range userRedirects {
		rr := regexp.MustCompile(redirect.From)
		match := rr.FindStringSubmatch(fp)
		if len(match) > 0 {
			ruleRoute := shared.GetAssetFileName(&utils.FileEntry{
				Filepath: filepath.Join(projectName, redirect.To),
			})
			rule := &HttpReply{
				Filepath: ruleRoute,
				Status:   redirect.Status,
				Query:    redirect.Query,
			}
			if redirect.Force {
				rts = append([]*HttpReply{rule}, rts...)
			} else {
				rts = append(rts, rule)
			}
		}
	}

	// user routes take precedence
	if len(rts) > 0 {
		rts = append(rts, notFound)
		return rts
	}

	// file extension is unknown
	if mimeType == "text/plain" && fext != ".txt" {
		dirRoute := shared.GetAssetFileName(&utils.FileEntry{
			Filepath: filepath.Join(projectName, fp, "index.html"),
		})
		// we need to accommodate routes that are just directories
		// and point the user to the index.html of each root dir.
		nameRoute := shared.GetAssetFileName(&utils.FileEntry{
			Filepath: filepath.Join(
				projectName,
				fdir,
				fmt.Sprintf("%s.html", fname),
			),
		})
		rts = append(rts,
			&HttpReply{Filepath: nameRoute, Status: 200},
			&HttpReply{Filepath: dirRoute, Status: 200},
			notFound,
		)
		return rts
	}

	defRoute := shared.GetAssetFileName(&utils.FileEntry{
		Filepath: filepath.Join(projectName, fdir, fname),
	})

	rts = append(rts,
		&HttpReply{
			Filepath: defRoute, Status: 200,
		},
		notFound,
	)

	return rts
}

func (h *AssetHandler) handle(w http.ResponseWriter) {
	var redirects []*RedirectRule
	redirectFp, _, _, err := h.Storage.GetObject(h.Bucket, filepath.Join(h.ProjectDir, "_redirects"))
	if err == nil {
		defer redirectFp.Close()
		buf := new(strings.Builder)
		_, err := io.Copy(buf, redirectFp)
		if err != nil {
			h.Logger.Error(err.Error())
			http.Error(w, "cannot read _redirect file", http.StatusInternalServerError)
			return
		}

		redirects, err = parseRedirectText(buf.String())
		if err != nil {
			h.Logger.Error(err.Error())
		}
	}

	routes := calcPossibleRoutes(h.ProjectDir, h.Filepath, redirects)
	var contents io.ReadCloser
	contentType := ""
	assetFilepath := ""
	status := 200
	attempts := []string{}
	for _, fp := range routes {
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
		h.Logger.Info(
			"asset not found in bucket",
			"bucket", h.Bucket.Name,
			"routes", strings.Join(attempts, ", "),
		)
		http.Error(w, "404 not found", http.StatusNotFound)
		return
	}
	defer contents.Close()

	if contentType == "" {
		contentType = storage.GetMimeType(assetFilepath)
	}

	w.Header().Add("Content-Type", contentType)
	w.WriteHeader(status)
	_, err = io.Copy(w, contents)

	if err != nil {
		h.Logger.Error(err.Error())
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
	logger := shared.GetLogger(r)
	cache := shared.GetCache(r)

	props, err := getProjectFromSubdomain(subdomain)
	if err != nil {
		logger.Info(err.Error(), "subdomain", subdomain, "filename", fname)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	user, err := dbpool.FindUserForName(props.Username)
	if err != nil {
		logger.Info("user not found", "user", props.Username)
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

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
			logger.Info(
				"project not found",
				"projectName", props.ProjectName,
			)
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}

		projectDir = project.ProjectDir
		if !hasPerm(project) {
			http.Error(w, "You do not have access to this site", http.StatusUnauthorized)
			return
		}
	}

	if err != nil {
		logger.Info("bucket not found", "user", props.Username)
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
		Cache:          cache,
		Bucket:         bucket,
		ImgProcessOpts: opts,
	}

	asset.handle(w)
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
			logger.Info(errMsg)
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
	shared.NewRoute("GET", "/rss", rssHandler),
	shared.NewRoute("GET", "/(.+)", shared.CreatePageHandler("html/marketing.page.tmpl")),
}

func createSubdomainRoutes(hasPerm HasPerm) []shared.Route {
	assetRequest := AssetRequest(hasPerm)
	imgRequest := ImgAssetRequest(hasPerm)

	return []shared.Route{
		shared.NewRoute("GET", "/", assetRequest),
		shared.NewRoute("GET", "(/.+.(?:jpg|jpeg|png|gif|webp|svg))(/.+)", imgRequest),
		shared.NewRoute("GET", "/(.+)", assetRequest),
	}
}

func publicPerm(proj *db.Project) bool {
	return proj.Acl.Type == "public"
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

	// cache resizing images since they are CPU-bound
	// we want to clear the cache since we are storing images
	// as []byte in-memory
	cache := gocache.New(2*time.Minute, 5*time.Minute)

	if err != nil {
		logger.Error(err.Error())
		return
	}

	handler := shared.CreateServe(mainRoutes, createSubdomainRoutes(publicPerm), cfg, db, st, logger, cache)
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
