package pgs

import (
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "net/http/pprof"

	"github.com/gorilla/feeds"
	gocache "github.com/patrickmn/go-cache"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/send/utils"
	"go.uber.org/zap"
)

type AssetHandler struct {
	Username   string
	Subdomain  string
	Filepath   string
	ProjectDir string
	Cfg        *shared.ConfigSite
	Dbpool     db.DB
	Storage    storage.ObjectStorage
	Logger     *zap.SugaredLogger
	Cache      *gocache.Cache
	UserID     string
}

func checkHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := shared.GetDB(r)
	cfg := shared.GetCfg(r)
	logger := shared.GetLogger(r)

	if cfg.IsCustomdomains() {
		hostDomain := r.URL.Query().Get("domain")
		appDomain := strings.Split(cfg.ConfigCms.Domain, ":")[0]

		if !strings.Contains(hostDomain, appDomain) {
			subdomain := shared.GetCustomDomain(logger, hostDomain, cfg.Space)
			props, err := getProjectFromSubdomain(subdomain)
			if err != nil {
				logger.Error(err)
				w.WriteHeader(http.StatusNotFound)
				return
			}

			u, err := dbpool.FindUserForName(props.Username)
			if err != nil {
				logger.Error(err)
				w.WriteHeader(http.StatusNotFound)
				return
			}

			p, err := dbpool.FindProjectByName(u.ID, props.ProjectName)
			if err != nil {
				logger.Error(err)
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
		logger.Error(err)
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
		logger.Fatal(err)
		http.Error(w, "Could not generate atom rss feed", http.StatusInternalServerError)
	}

	w.Header().Add("Content-Type", "application/atom+xml")
	_, err = w.Write([]byte(rss))
	if err != nil {
		logger.Error(err)
	}
}

func calcPossibleRoutes(projectName, fp string) []string {
	fname := filepath.Base(fp)
	fdir := filepath.Dir(fp)
	fext := filepath.Ext(fp)

	// hack: we need to accommodate routes that are just directories
	// and point the user to the index.html of each root dir.
	if fname == "." || fext == "" {
		return []string{
			shared.GetAssetFileName(&utils.FileEntry{
				Filepath: filepath.Join(projectName, fp, "index.html"),
			}),
			shared.GetAssetFileName(&utils.FileEntry{
				Filepath: filepath.Join(
					projectName,
					fdir,
					fmt.Sprintf("%s.html", fname),
				),
			}),
		}
	}

	return []string{
		shared.GetAssetFileName(&utils.FileEntry{
			Filepath: filepath.Join(projectName, fdir, fname),
		}),
		shared.GetAssetFileName(&utils.FileEntry{
			Filepath: filepath.Join(projectName, fp, "index.html"),
		}),
	}
}

func assetHandler(w http.ResponseWriter, h *AssetHandler) {
	bucket, err := h.Storage.GetBucket(shared.GetAssetBucketName(h.UserID))
	if err != nil {
		h.Logger.Infof("bucket not found for %s", h.Username)
		http.Error(w, "bucket not found", http.StatusNotFound)
		return
	}

	routes := calcPossibleRoutes(h.ProjectDir, h.Filepath)
	var contents storage.ReaderAtCloser
	assetFilepath := ""
	for _, fp := range routes {
		c, err := h.Storage.GetFile(bucket, fp)
		if err == nil {
			contents = c
			assetFilepath = fp
			break
		}
	}

	if assetFilepath == "" {
		h.Logger.Infof(
			"asset not found in bucket: bucket:[%s], file:[%s]",
			bucket.Name,
			h.Filepath,
		)
		http.Error(w, "404 not found", http.StatusNotFound)
		return
	}
	defer contents.Close()

	contentType := shared.GetMimeType(assetFilepath)
	w.Header().Add("Content-Type", contentType)
	_, err = io.Copy(w, contents)

	if err != nil {
		h.Logger.Error(err)
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

func serveAsset(subdomain string, w http.ResponseWriter, r *http.Request) {
	cfg := shared.GetCfg(r)
	dbpool := shared.GetDB(r)
	st := shared.GetStorage(r)
	logger := shared.GetLogger(r)
	cache := shared.GetCache(r)

	floc, _ := url.PathUnescape(shared.GetField(r, 0))
	props, err := getProjectFromSubdomain(subdomain)
	if err != nil {
		logger.Info(err)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	user, err := dbpool.FindUserForName(props.Username)
	if err != nil {
		logger.Infof("user not found: %s", props.Username)
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	projectDir := props.ProjectName
	project, err := dbpool.FindProjectByName(user.ID, props.ProjectName)
	if err == nil {
		projectDir = project.ProjectDir
	}

	assetHandler(w, &AssetHandler{
		Username:   props.Username,
		UserID:     user.ID,
		Subdomain:  subdomain,
		ProjectDir: projectDir,
		Filepath:   floc,
		Cfg:        cfg,
		Dbpool:     dbpool,
		Storage:    st,
		Logger:     logger,
		Cache:      cache,
	})
}

func marketingRequest(w http.ResponseWriter, r *http.Request) {
	subdomain := "hey-pgs-prod"
	serveAsset(subdomain, w, r)
}

func assetRequest(w http.ResponseWriter, r *http.Request) {
	subdomain := shared.GetSubdomain(r)
	serveAsset(subdomain, w, r)
}

func StartApiServer() {
	cfg := NewConfigSite()
	logger := cfg.Logger

	db := postgres.NewDB(cfg.DbURL, cfg.Logger)
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

	mainRoutes := []shared.Route{
		shared.NewRoute("GET", "/", marketingRequest),
		shared.NewRoute("GET", "/check", checkHandler),
		shared.NewRoute("GET", "/rss", rssHandler),
		shared.NewRoute("GET", "/(.+)", marketingRequest),
	}
	subdomainRoutes := []shared.Route{
		shared.NewRoute("GET", "/", assetRequest),
		shared.NewRoute("GET", "/(.+)", assetRequest),
	}

	handler := shared.CreateServe(mainRoutes, subdomainRoutes, cfg, db, st, logger, cache)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Infof("Starting server on port %s", cfg.Port)
	logger.Infof("Subdomains enabled: %t", cfg.SubdomainsEnabled)
	logger.Infof("Domain: %s", cfg.Domain)
	logger.Infof("Email: %s", cfg.Email)

	logger.Fatal(http.ListenAndServe(portStr, router))
}
