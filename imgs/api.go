package imgs

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"time"

	_ "net/http/pprof"

	"github.com/gorilla/feeds"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/imgs/storage"
	"github.com/picosh/pico/shared"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
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

func GetPostTitle(post *db.Post) string {
	if post.Description == "" {
		return post.Title
	}

	return fmt.Sprintf("%s: %s", post.Title, post.Description)
}

func GetBlogName(username string) string {
	return username
}

func blogHandler(w http.ResponseWriter, r *http.Request) {
	username := shared.GetUsernameFromRequest(r)
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	cfg := shared.GetCfg(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("blog not found: %s", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	tag := r.URL.Query().Get("tag")
	var posts []*db.Post
	var p *db.Paginate[*db.Post]
	pager := &db.Pager{Num: 1000, Page: 0}
	if tag == "" {
		p, err = dbpool.FindPostsForUser(pager, user.ID, cfg.Space)
	} else {
		p, err = dbpool.FindUserPostsByTag(pager, tag, user.ID, cfg.Space)
	}
	posts = p.Data

	if err != nil {
		logger.Error(err)
		http.Error(w, "could not fetch posts for blog", http.StatusInternalServerError)
		return
	}

	ts, err := shared.RenderTemplate(cfg, []string{
		cfg.StaticPath("html/blog.page.tmpl"),
	})

	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	headerTxt := &HeaderTxt{
		Title: GetBlogName(username),
		Bio:   "",
	}
	readmeTxt := &ReadmeTxt{}

	curl := shared.CreateURLFromRequest(cfg, r)
	postCollection := make([]*PostItemData, 0, len(posts))
	for _, post := range posts {
		url := fmt.Sprintf(
			"%s/300x",
			cfg.ImgURL(curl, post.Username, post.Slug),
		)
		postCollection = append(postCollection, &PostItemData{
			ImgURL:       template.URL(url),
			URL:          template.URL(cfg.ImgPostURL(curl, post.Username, post.Slug)),
			Caption:      post.Title,
			PublishAt:    post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO: post.PublishAt.Format(time.RFC3339),
		})
	}

	data := BlogPageData{
		Site:      *cfg.GetSiteData(),
		PageTitle: headerTxt.Title,
		URL:       template.URL(cfg.FullBlogURL(curl, username)),
		RSSURL:    template.URL(cfg.RssBlogURL(curl, username, tag)),
		Readme:    readmeTxt,
		Header:    headerTxt,
		Username:  username,
		Posts:     postCollection,
		HasFilter: tag != "",
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type ImgHandler struct {
	Username  string
	Subdomain string
	Slug      string
	Cfg       *shared.ConfigSite
	Dbpool    db.DB
	Storage   storage.ObjectStorage
	Logger    *zap.SugaredLogger
	Img       *shared.ImgOptimizer
	// We should try to use the optimized image if it's available
	// not all images are optimized so this flag isn't enough
	// because we also need to check the mime type
	UseOptimized bool
}

func imgHandler(w http.ResponseWriter, h *ImgHandler) {
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
	isWebOptimized := shared.IsWebOptimized(contentType)

	if h.UseOptimized && isWebOptimized {
		contentType = "image/webp"
		fname = fmt.Sprintf("%s.webp", shared.SanitizeFileExt(post.Filename))
	}

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

	resizeImg := h.Img.Width != 0 || h.Img.Height != 0

	if h.UseOptimized && resizeImg && isWebOptimized {
		// when resizing an image we don't want to mess with quality
		// since that was already applied when converting to webp
		h.Img.Quality = 100
		h.Img.Lossless = false
		err = h.Img.Process(w, contents)
	} else {
		_, err = io.Copy(w, contents)
	}

	if err != nil {
		h.Logger.Error(err)
	}
}

func imgRequestOriginal(w http.ResponseWriter, r *http.Request) {
	username := shared.GetUsernameFromRequest(r)
	subdomain := shared.GetSubdomain(r)
	cfg := shared.GetCfg(r)

	var slug string
	if !cfg.IsSubdomains() || subdomain == "" {
		slug, _ = url.PathUnescape(shared.GetField(r, 1))
	} else {
		slug, _ = url.PathUnescape(shared.GetField(r, 0))
	}

	// users might add the file extension when requesting an image
	// but we want to remove that
	slug = shared.SanitizeFileExt(slug)

	dbpool := shared.GetDB(r)
	st := shared.GetStorage(r)
	logger := shared.GetLogger(r)

	imgHandler(w, &ImgHandler{
		Username:     username,
		Subdomain:    subdomain,
		Slug:         slug,
		Cfg:          cfg,
		Dbpool:       dbpool,
		Storage:      st,
		Logger:       logger,
		Img:          shared.NewImgOptimizer(logger, ""),
		UseOptimized: false,
	})
}

func imgRequest(w http.ResponseWriter, r *http.Request) {
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

	imgHandler(w, &ImgHandler{
		Username:     username,
		Subdomain:    subdomain,
		Slug:         slug,
		Cfg:          cfg,
		Dbpool:       dbpool,
		Storage:      st,
		Logger:       logger,
		Img:          shared.NewImgOptimizer(logger, dimes),
		UseOptimized: true,
	})
}

func postHandler(w http.ResponseWriter, r *http.Request) {
	username := shared.GetUsernameFromRequest(r)
	subdomain := shared.GetSubdomain(r)
	cfg := shared.GetCfg(r)

	var slug string
	if !cfg.IsSubdomains() || subdomain == "" {
		slug, _ = url.PathUnescape(shared.GetField(r, 1))
	} else {
		slug, _ = url.PathUnescape(shared.GetField(r, 0))
	}

	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("blog not found: %s", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	blogName := GetBlogName(username)
	curl := shared.CreateURLFromRequest(cfg, r)

	var data PostPageData
	post, err := dbpool.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err == nil {
		linkify := NewImgsLinkify(username)
		parsed, err := shared.ParseText(post.Text, linkify)
		if err != nil {
			logger.Error(err)
		}
		text := ""
		if parsed != nil {
			text = parsed.Html
		}

		tagLinks := make([]Link, 0, len(post.Tags))
		for _, tag := range post.Tags {
			tagLinks = append(tagLinks, Link{
				URL:  template.URL(cfg.TagURL(curl, username, tag)),
				Text: tag,
			})
		}

		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			PageTitle:    GetPostTitle(post),
			URL:          template.URL(cfg.FullPostURL(curl, post.Username, post.Slug)),
			BlogURL:      template.URL(cfg.FullBlogURL(curl, username)),
			Caption:      post.Description,
			Title:        post.Title,
			Slug:         post.Slug,
			PublishAt:    post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO: post.PublishAt.Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Contents:     template.HTML(text),
			ImgURL:       template.URL(cfg.ImgURL(curl, username, post.Slug)),
			Tags:         tagLinks,
		}
	} else {
		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			BlogURL:      template.URL(cfg.FullBlogURL(curl, username)),
			PageTitle:    "Post not found",
			Caption:      "Post not found",
			Title:        "Post not found",
			PublishAt:    time.Now().Format("02 Jan, 2006"),
			PublishAtISO: time.Now().Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
		}
		logger.Infof("post not found %s/%s", username, slug)
	}

	ts, err := shared.RenderTemplate(cfg, []string{
		cfg.StaticPath("html/post.page.tmpl"),
	})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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

func rssBlogHandler(w http.ResponseWriter, r *http.Request) {
	username := shared.GetUsernameFromRequest(r)
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	cfg := shared.GetCfg(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("rss feed not found: %s", username)
		http.Error(w, "rss feed not found", http.StatusNotFound)
		return
	}

	tag := r.URL.Query().Get("tag")
	var posts []*db.Post
	var p *db.Paginate[*db.Post]
	pager := &db.Pager{Num: 10, Page: 0}
	if tag == "" {
		p, err = dbpool.FindPostsForUser(pager, user.ID, cfg.Space)
	} else {
		p, err = dbpool.FindUserPostsByTag(pager, tag, user.ID, cfg.Space)
	}
	posts = p.Data

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

	headerTxt := &HeaderTxt{
		Title: GetBlogName(username),
	}

	curl := shared.CreateURLFromRequest(cfg, r)

	feed := &feeds.Feed{
		Title:       headerTxt.Title,
		Link:        &feeds.Link{Href: cfg.FullBlogURL(curl, username)},
		Description: headerTxt.Bio,
		Author:      &feeds.Author{Name: username},
		Created:     time.Now(),
	}

	var feedItems []*feeds.Item
	for _, post := range posts {
		if slices.Contains(cfg.HiddenPosts, post.Filename) {
			continue
		}
		var tpl bytes.Buffer
		data := &PostPageData{
			ImgURL: template.URL(cfg.ImgURL(curl, username, post.Slug)),
		}
		if err := ts.Execute(&tpl, data); err != nil {
			continue
		}

		realUrl := cfg.FullPostURL(curl, post.Username, post.Slug)
		if !curl.Subdomain && !curl.UsernameInRoute {
			realUrl = fmt.Sprintf("%s://%s%s", cfg.Protocol, r.Host, realUrl)
		}

		item := &feeds.Item{
			Id:          realUrl,
			Title:       post.Title,
			Link:        &feeds.Link{Href: realUrl},
			Created:     *post.PublishAt,
			Content:     tpl.String(),
			Updated:     *post.UpdatedAt,
			Description: post.Description,
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

func rssHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	cfg := shared.GetCfg(r)

	pager, err := dbpool.FindAllPosts(&db.Pager{Num: 25, Page: 0}, cfg.Space)
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
		Link:        &feeds.Link{Href: cfg.ReadURL()},
		Description: fmt.Sprintf("%s latest image", cfg.Domain),
		Author:      &feeds.Author{Name: cfg.Domain},
		Created:     time.Now(),
	}

	curl := shared.CreateURLFromRequest(cfg, r)

	var feedItems []*feeds.Item
	for _, post := range pager.Data {
		var tpl bytes.Buffer
		data := &PostPageData{
			ImgURL: template.URL(cfg.ImgURL(curl, post.Username, post.Slug)),
		}
		if err := ts.Execute(&tpl, data); err != nil {
			continue
		}

		realUrl := cfg.FullPostURL(curl, post.Username, post.Slug)
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

	routes = append(
		routes,
		shared.NewRoute("GET", "/rss", rssHandler),
		shared.NewRoute("GET", "/rss.xml", rssHandler),
		shared.NewRoute("GET", "/atom.xml", rssHandler),
		shared.NewRoute("GET", "/feed.xml", rssHandler),

		shared.NewRoute("GET", "/([^/]+)", blogHandler),
		shared.NewRoute("GET", "/([^/]+)/rss", rssBlogHandler),
		shared.NewRoute("GET", "/([^/]+)/rss.xml", rssBlogHandler),
		shared.NewRoute("GET", "/([^/]+)/atom.xml", rssBlogHandler),
		shared.NewRoute("GET", "/([^/]+)/feed.xml", rssBlogHandler),
		shared.NewRoute("GET", "/([^/]+)/o/([^/]+)", imgRequestOriginal),
		shared.NewRoute("GET", "/([^/]+)/p/([^/]+)", postHandler),
		shared.NewRoute("GET", "/([^/]+)/([^/]+)", imgRequest),
		shared.NewRoute("GET", "/([^/]+)/([^/]+)/([a-z0-9]+)", imgRequest),
	)

	return routes
}

func createSubdomainRoutes(staticRoutes []shared.Route) []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "/", blogHandler),
		shared.NewRoute("GET", "/rss", rssBlogHandler),
		shared.NewRoute("GET", "/rss.xml", rssBlogHandler),
		shared.NewRoute("GET", "/atom.xml", rssBlogHandler),
		shared.NewRoute("GET", "/feed.xml", rssBlogHandler),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		shared.NewRoute("GET", "/o/([^/]+)", imgRequestOriginal),
		shared.NewRoute("GET", "/p/([^/]+)", postHandler),
		shared.NewRoute("GET", "/([^/]+)", imgRequest),
		shared.NewRoute("GET", "/([^/]+)/([a-z0-9]+)", imgRequest),
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

	if err != nil {
		logger.Fatal(err)
	}

	staticRoutes := createStaticRoutes()

	if cfg.Debug {
		staticRoutes = shared.CreatePProfRoutes(staticRoutes)
	}

	mainRoutes := createMainRoutes(staticRoutes)
	subdomainRoutes := createSubdomainRoutes(staticRoutes)

	handler := shared.CreateServe(mainRoutes, subdomainRoutes, cfg, db, st, logger)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Infof("Starting server on port %s", cfg.Port)
	logger.Infof("Subdomains enabled: %t", cfg.SubdomainsEnabled)
	logger.Infof("Domain: %s", cfg.Domain)
	logger.Infof("Email: %s", cfg.Email)

	logger.Fatal(http.ListenAndServe(portStr, router))
}
