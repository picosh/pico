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
	gocache "github.com/patrickmn/go-cache"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/pgs"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
)

type PostItemData struct {
	BlogURL      template.URL
	URL          template.URL
	ImgURL       template.URL
	PublishAtISO string
	PublishAt    string
	Caption      string
}

type PostPageData struct {
	ImgURL template.URL
}

var Space = "imgs"

func rssHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	cfg := shared.GetCfg(r)

	pager, err := dbpool.FindAllPosts(&db.Pager{Num: 25, Page: 0}, Space)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := template.ParseFiles(cfg.StaticPath("html/rss.page.tmpl"))
	if err != nil {
		logger.Error(err)
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
		logger.Fatal(err)
		http.Error(w, "Could not generate atom rss feed", http.StatusInternalServerError)
	}

	w.Header().Add("Content-Type", "application/atom+xml")
	_, err = w.Write([]byte(rss))
	if err != nil {
		logger.Error(err)
	}
}

func ImgRequest(w http.ResponseWriter, r *http.Request) {
	subdomain := shared.GetSubdomain(r)
	cfg := shared.GetCfg(r)
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	username := shared.GetUsernameFromRequest(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("rss feed not found: %s", username)
		http.Error(w, "rss feed not found", http.StatusNotFound)
		return
	}

	var dimes string
	var slug string
	if !cfg.IsSubdomains() || subdomain == "" {
		slug, _ = url.PathUnescape(shared.GetField(r, 1))
		dimes, _ = url.PathUnescape(shared.GetField(r, 2))
	} else {
		slug, _ = url.PathUnescape(shared.GetField(r, 0))
		dimes, _ = url.PathUnescape(shared.GetField(r, 1))
	}

	ratio, _ := storage.GetRatio(dimes)
	opts := &storage.ImgProcessOpts{
		Quality: 80,
		Ratio:   ratio,
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
		logger.Infof(errMsg)
		http.Error(w, errMsg, http.StatusNotFound)
		return
	}

	fname := post.Filename
	pgs.ServeAsset(fname, opts, true, w, r)
}

func FindImgPost(r *http.Request, user *db.User, slug string) (*db.Post, error) {
	dbpool := shared.GetDB(r)
	return dbpool.FindPostWithSlug(slug, user.ID, Space)
}

func createMainRoutes(staticRoutes []shared.Route) []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "/check", shared.CheckHandler),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		shared.NewRoute("GET", "/rss", rssHandler),
		shared.NewRoute("GET", "/rss.xml", rssHandler),
		shared.NewRoute("GET", "/atom.xml", rssHandler),
		shared.NewRoute("GET", "/feed.xml", rssHandler),

		shared.NewRoute("GET", "/([^/]+)/o/([^/]+)", ImgRequest),
		shared.NewRoute("GET", "/([^/]+)/([^/]+)", ImgRequest),
		shared.NewRoute("GET", "/([^/]+)/([^/]+)/([a-z0-9]+)", ImgRequest),
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
		shared.NewRoute("GET", "/o/([^/]+)", ImgRequest),
		shared.NewRoute("GET", "/([^/]+)", ImgRequest),
		shared.NewRoute("GET", "/([^/]+)/([a-z0-9]+)", ImgRequest),
	)

	return routes
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

	staticRoutes := []shared.Route{}
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
