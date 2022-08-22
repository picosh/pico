package imgs

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/db/postgres"
	"git.sr.ht/~erock/pico/imgs/storage"
	"git.sr.ht/~erock/pico/shared"
	"github.com/gorilla/feeds"
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

func isRequestTrackable(r *http.Request) bool {
	return true
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
	if tag == "" {
		posts, err = dbpool.FindPostsForUser(user.ID, cfg.Space)
	} else {
		posts, err = dbpool.FindUserPostsByTag(tag, user.ID, cfg.Space)
	}

	if err != nil {
		logger.Error(err)
		http.Error(w, "could not fetch posts for blog", http.StatusInternalServerError)
		return
	}

	hostDomain := strings.Split(r.Host, ":")[0]
	appDomain := strings.Split(cfg.ConfigCms.Domain, ":")[0]

	onSubdomain := cfg.IsSubdomains() && strings.Contains(hostDomain, appDomain)
	withUserName := (!onSubdomain && hostDomain == appDomain) || !cfg.IsCustomdomains()

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

	postCollection := make([]*PostItemData, 0, len(posts))
	for _, post := range posts {
		postCollection = append(postCollection, &PostItemData{
			ImgURL:       template.URL(cfg.ImgURL(post.Username, post.Filename, onSubdomain, withUserName)),
			URL:          template.URL(cfg.ImgURL(post.Username, post.Slug, onSubdomain, withUserName)),
			Caption:      post.Title,
			PublishAt:    post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO: post.PublishAt.Format(time.RFC3339),
		})
	}

	data := BlogPageData{
		Site:      *cfg.GetSiteData(),
		PageTitle: headerTxt.Title,
		URL:       template.URL(cfg.FullBlogURL(username, onSubdomain, withUserName)),
		RSSURL:    template.URL(cfg.RssBlogURL(username, onSubdomain, withUserName, tag)),
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

func imgHandler(w http.ResponseWriter, r *http.Request) {
	username := shared.GetUsernameFromRequest(r)
	subdomain := shared.GetSubdomain(r)
	cfg := shared.GetCfg(r)

	var filename string
	if !cfg.IsSubdomains() || subdomain == "" {
		filename, _ = url.PathUnescape(shared.GetField(r, 1))
	} else {
		filename, _ = url.PathUnescape(shared.GetField(r, 0))
	}

	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("blog not found: %s", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	post, err := dbpool.FindPostWithFilename(filename, user.ID, cfg.Space)
	if err != nil {
		logger.Infof("image not found %s/%s", username, filename)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// validate and fire off analytic event
	if isRequestTrackable(r) {
		_, err := dbpool.AddViewCount(post.ID)
		if err != nil {
			logger.Error(err)
		}
	}

	st := storage.NewStorageFS(cfg.StorageDir)
	bucket, err := st.GetBucket(user.ID)
	if err != nil {
		logger.Infof("bucket not found %s/%s", username, filename)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	contents, err := st.GetFile(bucket, post.Filename)
	if err != nil {
		logger.Infof("file not found %s/%s", username, post.Filename)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Add("Content-Type", "image/png")
	_, err = w.Write(contents)
	if err != nil {
		logger.Error(err)
	}
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
	hostDomain := strings.Split(r.Host, ":")[0]
	appDomain := strings.Split(cfg.ConfigCms.Domain, ":")[0]

	onSubdomain := cfg.IsSubdomains() && strings.Contains(hostDomain, appDomain)
	withUserName := (!onSubdomain && hostDomain == appDomain) || !cfg.IsCustomdomains()

	var data PostPageData
	post, err := dbpool.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err == nil {
		parsed, err := shared.ParseText(post.Text, cfg.ImgURL(username, "", true, false))
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
				URL:  template.URL(cfg.TagURL(username, tag, onSubdomain, withUserName)),
				Text: tag,
			})
		}

		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			PageTitle:    GetPostTitle(post),
			URL:          template.URL(cfg.FullPostURL(post.Username, post.Slug, onSubdomain, withUserName)),
			BlogURL:      template.URL(cfg.FullBlogURL(username, onSubdomain, withUserName)),
			Caption:      post.Description,
			Title:        post.Title,
			Slug:         post.Slug,
			PublishAt:    post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO: post.PublishAt.Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Contents:     template.HTML(text),
			ImgURL:       template.URL(cfg.ImgURL(username, post.Filename, onSubdomain, withUserName)),
			Tags:         tagLinks,
		}
	} else {
		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			BlogURL:      template.URL(cfg.FullBlogURL(username, onSubdomain, withUserName)),
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

	posts, err := dbpool.FindPostsForUser(user.ID, cfg.Space)

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

	hostDomain := strings.Split(r.Host, ":")[0]
	appDomain := strings.Split(cfg.ConfigCms.Domain, ":")[0]

	onSubdomain := cfg.IsSubdomains() && strings.Contains(hostDomain, appDomain)
	withUserName := (!onSubdomain && hostDomain == appDomain) || !cfg.IsCustomdomains()

	feed := &feeds.Feed{
		Title:       headerTxt.Title,
		Link:        &feeds.Link{Href: cfg.FullBlogURL(username, onSubdomain, withUserName)},
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
			ImgURL: template.URL(cfg.ImgURL(username, post.Filename, onSubdomain, withUserName)),
		}
		if err := ts.Execute(&tpl, data); err != nil {
			continue
		}

		realUrl := cfg.FullPostURL(post.Username, post.Slug, onSubdomain, withUserName)
		if !onSubdomain && !withUserName {
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

	hostDomain := strings.Split(r.Host, ":")[0]
	appDomain := strings.Split(cfg.ConfigCms.Domain, ":")[0]

	onSubdomain := cfg.IsSubdomains() && strings.Contains(hostDomain, appDomain)
	withUserName := (!onSubdomain && hostDomain == appDomain) || !cfg.IsCustomdomains()

	var feedItems []*feeds.Item
	for _, post := range pager.Data {
		var tpl bytes.Buffer
		data := &PostPageData{
			ImgURL: template.URL(cfg.ImgURL(post.Username, post.Filename, onSubdomain, withUserName)),
		}
		if err := ts.Execute(&tpl, data); err != nil {
			continue
		}

		realUrl := cfg.FullPostURL(post.Username, post.Slug, onSubdomain, withUserName)
		if !onSubdomain && !withUserName {
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
		shared.NewRoute("GET", "/([^/]+)/([^/]+\\..+)", imgHandler),
		shared.NewRoute("GET", "/([^/]+)/([^/]+)", postHandler),
	)

	return routes
}

func createSubdomainRoutes(staticRoutes []shared.Route) []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "/", blogHandler),
		shared.NewRoute("GET", "/rss", rssBlogHandler),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		shared.NewRoute("GET", "/([^/]+\\..+)", imgHandler),
		shared.NewRoute("GET", "/([^/]+)", postHandler),
	)

	return routes
}

func StartApiServer() {
	cfg := NewConfigSite()
	db := postgres.NewDB(&cfg.ConfigCms)
	defer db.Close()
	logger := cfg.Logger

	staticRoutes := createStaticRoutes()
	mainRoutes := createMainRoutes(staticRoutes)
	subdomainRoutes := createSubdomainRoutes(staticRoutes)

	handler := shared.CreateServe(mainRoutes, subdomainRoutes, cfg, db, logger)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Infof("Starting server on port %s", cfg.Port)
	logger.Infof("Subdomains enabled: %t", cfg.SubdomainsEnabled)
	logger.Infof("Domain: %s", cfg.Domain)
	logger.Infof("Email: %s", cfg.Email)

	logger.Fatal(http.ListenAndServe(portStr, router))
}
