package buckets

import (
	"fmt"
	"io"
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

type AssetHandler struct {
	Username  string
	Subdomain string
	Path      string
	Filename  string
	Cfg       *shared.ConfigSite
	Dbpool    db.DB
	Storage   storage.ObjectStorage
	Logger    *zap.SugaredLogger
	Cache     *gocache.Cache
}

func assetHandler(w http.ResponseWriter, h *AssetHandler) {
	user, err := h.Dbpool.FindUserForName(h.Username)
	if err != nil {
		h.Logger.Infof("blog not found: %s", h.Username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	post, err := h.Dbpool.FindPostWithPath(fmt.Sprintf("/%s", h.Path), h.Filename, user.ID, h.Cfg.Space)
	if err != nil {
		h.Logger.Infof("asset not found %s/%s/%s", h.Username, h.Path, h.Filename)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_, err = h.Dbpool.AddViewCount(post.ID)
	if err != nil {
		h.Logger.Error(err)
	}

	bucket, err := h.Storage.GetBucket(shared.GetAssetBucketName(user.ID))
	if err != nil {
		h.Logger.Infof("bucket not found %s/%s", h.Username, post.Filename)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	contentType := post.MimeType
	fname := shared.GetAssetFileName(post.Path, post.Filename)

	contents, err := h.Storage.GetFile(bucket, fname)
	if err != nil {
		h.Logger.Infof(
			"file not found %s/%s in storage (bucket: %s, name: %s)",
			h.Username,
			fname,
			bucket.Name,
			fname,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer contents.Close()

	w.Header().Add("Content-Type", contentType)
	_, err = io.Copy(w, contents)

	if err != nil {
		h.Logger.Error(err)
	}
}

func assetRequest(w http.ResponseWriter, r *http.Request) {
	username := shared.GetUsernameFromRequest(r)
	subdomain := shared.GetSubdomain(r)
	cfg := shared.GetCfg(r)

	var fpath string
	var fname string
	if !cfg.IsSubdomains() || subdomain == "" {
		fpath, _ = url.PathUnescape(shared.GetField(r, 1))
		fname, _ = url.PathUnescape(shared.GetField(r, 2))
	} else {
		fpath, _ = url.PathUnescape(shared.GetField(r, 0))
		fname, _ = url.PathUnescape(shared.GetField(r, 1))
	}

	dbpool := shared.GetDB(r)
	st := shared.GetStorage(r)
	logger := shared.GetLogger(r)
	cache := shared.GetCache(r)

	assetHandler(w, &AssetHandler{
		Username:  username,
		Subdomain: subdomain,
		Filename:  fname,
		Path:      fpath,
		Cfg:       cfg,
		Dbpool:    dbpool,
		Storage:   st,
		Logger:    logger,
		Cache:     cache,
	})
}

func createSubdomainRoutes(staticRoutes []shared.Route) []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "/(.+)", assetRequest),
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


	mainRoutes := []shared.Route{}
	subdomainRoutes := createSubdomainRoutes([]shared.Route{})

	handler := shared.CreateServe(mainRoutes, subdomainRoutes, cfg, db, st, logger, cache)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Infof("Starting server on port %s", cfg.Port)
	logger.Infof("Subdomains enabled: %t", cfg.SubdomainsEnabled)
	logger.Infof("Domain: %s", cfg.Domain)
	logger.Infof("Email: %s", cfg.Email)

	logger.Fatal(http.ListenAndServe(portStr, router))
}
