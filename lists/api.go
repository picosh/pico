package lists

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/gorilla/feeds"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/imgs"
	"github.com/picosh/pico/imgs/storage"
	"github.com/picosh/pico/shared"
	"golang.org/x/exp/slices"
)

type PostItemData struct {
	URL            template.URL
	BlogURL        template.URL
	Username       string
	Title          string
	Description    string
	PublishAtISO   string
	PublishAt      string
	UpdatedAtISO   string
	UpdatedTimeAgo string
	Padding        string
}

type BlogPageData struct {
	Site      shared.SitePageData
	PageTitle string
	URL       template.URL
	RSSURL    template.URL
	Username  string
	Readme    *ReadmeTxt
	Header    *HeaderTxt
	Posts     []PostItemData
	HasFilter bool
}

type ReadPageData struct {
	Site      shared.SitePageData
	NextPage  string
	PrevPage  string
	Posts     []PostItemData
	Tags      []string
	HasFilter bool
}

type PostPageData struct {
	Site         shared.SitePageData
	PageTitle    string
	URL          template.URL
	BlogURL      template.URL
	Title        string
	Description  string
	Username     string
	BlogName     string
	ListType     string
	Items        []*ListItem
	PublishAtISO string
	PublishAt    string
	Tags         []string
}

type TransparencyPageData struct {
	Site      shared.SitePageData
	Analytics *db.Analytics
}

type HeaderTxt struct {
	Title    string
	Bio      string
	Nav      []*ListItem
	Layout   string
	HasItems bool
}

type ReadmeTxt struct {
	HasItems bool
	ListType string
	Items    []*ListItem
}

func getPostsForUser(r *http.Request, user *db.User, tag string, num int) ([]*db.Post, error) {
	dbpool := shared.GetDB(r)
	cfg := shared.GetCfg(r)
	var err error

	posts := make([]*db.Post, 0)
	pager := &db.Pager{Num: num, Page: 0}
	var p *db.Paginate[*db.Post]
	if tag == "" {
		p, err = dbpool.FindPostsForUser(pager, user.ID, cfg.Space)
	} else {
		p, err = dbpool.FindUserPostsByTag(pager, tag, user.ID, cfg.Space)
	}
	posts = p.Data

	if err != nil {
		return posts, err
	}

	sort.Slice(posts, func(i, j int) bool {
		return posts[i].UpdatedAt.After(*posts[j].UpdatedAt)
	})

	return posts, nil
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
	posts, err := getPostsForUser(r, user, tag, 1000)
	if err != nil {
		logger.Error(err)
		http.Error(w, "could not fetch posts for blog", http.StatusInternalServerError)
		return
	}

	curl := shared.CreateURLFromRequest(cfg, r)

	ts, err := shared.RenderTemplate(cfg, []string{
		cfg.StaticPath("html/blog-default.partial.tmpl"),
		cfg.StaticPath("html/blog-aside.partial.tmpl"),
		cfg.StaticPath("html/blog.page.tmpl"),
		cfg.StaticPath("html/list.partial.tmpl"),
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
	header, err := dbpool.FindPostWithFilename("_header.txt", user.ID, cfg.Space)
	if err == nil {
		linkify := imgs.NewImgsLinkify(username)
		parsedText := ParseText(header.Text, linkify)
		if parsedText.MetaData.Title != "" {
			headerTxt.Title = parsedText.MetaData.Title
		}

		if parsedText.MetaData.Description != "" {
			headerTxt.Bio = parsedText.MetaData.Description
		}

		if parsedText.MetaData.Layout != "" {
			headerTxt.Layout = parsedText.MetaData.Layout
		}

		headerTxt.Nav = parsedText.Items
		if len(headerTxt.Nav) > 0 {
			headerTxt.HasItems = true
		}
	}

	readmeTxt := &ReadmeTxt{}
	readme, err := dbpool.FindPostWithFilename("_readme.txt", user.ID, cfg.Space)
	if err == nil {
		linkify := imgs.NewImgsLinkify(username)
		parsedText := ParseText(readme.Text, linkify)
		readmeTxt.Items = parsedText.Items
		readmeTxt.ListType = parsedText.MetaData.ListType
		if len(readmeTxt.Items) > 0 {
			readmeTxt.HasItems = true
		}
	}

	postCollection := make([]PostItemData, 0, len(posts))
	for _, post := range posts {
		p := PostItemData{
			URL:            template.URL(cfg.FullPostURL(curl, post.Username, post.Slug)),
			BlogURL:        template.URL(cfg.FullBlogURL(curl, post.Username)),
			Title:          shared.FilenameToTitle(post.Filename, post.Title),
			PublishAt:      post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: shared.TimeAgo(post.UpdatedAt),
			UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
		}
		postCollection = append(postCollection, p)
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

func GetPostTitle(post *db.Post) string {
	if post.Description == "" {
		return post.Title
	}

	return fmt.Sprintf("%s: %s", post.Title, post.Description)
}

func GetBlogName(username string) string {
	return fmt.Sprintf("%s's lists", username)
}

func postRawHandler(w http.ResponseWriter, r *http.Request) {
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

	post, err := dbpool.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err != nil {
		logger.Infof("post not found")
		http.Error(w, "post not found", http.StatusNotFound)
		return
	}

	w.Header().Add("Content-Type", "text/plain")

	_, err = w.Write([]byte(post.Text))
	if err != nil {
		logger.Error(err)
		http.Error(w, "server error", 500)
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

	header, _ := dbpool.FindPostWithFilename("_header.txt", user.ID, cfg.Space)
	blogName := GetBlogName(username)
	linkify := imgs.NewImgsLinkify(username)
	if header != nil {
		headerParsed := ParseText(header.Text, linkify)
		if headerParsed.MetaData.Title != "" {
			blogName = headerParsed.MetaData.Title
		}
	}

	var data PostPageData
	post, err := dbpool.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err == nil {
		parsedText := ParseText(post.Text, linkify)

		// we need the blog name from the readme unfortunately
		readme, err := dbpool.FindPostWithFilename("_readme.txt", user.ID, cfg.Space)
		if err == nil {
			readmeParsed := ParseText(readme.Text, linkify)
			if readmeParsed.MetaData.Title != "" {
				blogName = readmeParsed.MetaData.Title
			}
		}

		// validate and fire off analytic event
		if isRequestTrackable(r) {
			_, err := dbpool.AddViewCount(post.ID)
			if err != nil {
				logger.Error(err)
			}
		}

		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			PageTitle:    GetPostTitle(post),
			URL:          template.URL(cfg.PostURL(post.Username, post.Slug)),
			BlogURL:      template.URL(cfg.BlogURL(username)),
			Description:  post.Description,
			ListType:     parsedText.MetaData.ListType,
			Title:        shared.FilenameToTitle(post.Filename, post.Title),
			PublishAt:    post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO: post.PublishAt.Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Items:        parsedText.Items,
			Tags:         parsedText.MetaData.Tags,
		}
	} else {
		logger.Infof("post not found %s/%s", username, slug)
		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			PageTitle:    "Post not found",
			Description:  "Post not found",
			Title:        "Post not found",
			ListType:     "none",
			BlogURL:      template.URL(cfg.BlogURL(username)),
			PublishAt:    time.Now().Format("02 Jan, 2006"),
			PublishAtISO: time.Now().Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Items: []*ListItem{
				{
					Value:  "oops!  we can't seem to find this post.",
					IsText: true,
				},
			},
		}
	}

	ts, err := shared.RenderTemplate(cfg, []string{
		cfg.StaticPath("html/post.page.tmpl"),
		cfg.StaticPath("html/list.partial.tmpl"),
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

func readHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	cfg := shared.GetCfg(r)

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	tag := r.URL.Query().Get("tag")
	var pager *db.Paginate[*db.Post]
	var err error
	if tag == "" {
		pager, err = dbpool.FindAllUpdatedPosts(&db.Pager{Num: 30, Page: page}, cfg.Space)
	} else {
		pager, err = dbpool.FindPostsByTag(&db.Pager{Num: 30, Page: page}, tag, cfg.Space)
	}

	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := shared.RenderTemplate(cfg, []string{
		cfg.StaticPath("html/read.page.tmpl"),
	})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	nextPage := ""
	if page < pager.Total-1 {
		nextPage = fmt.Sprintf("/read?page=%d", page+1)
		if tag != "" {
			nextPage = fmt.Sprintf("%s&tag=%s", nextPage, tag)
		}
	}

	prevPage := ""
	if page > 0 {
		prevPage = fmt.Sprintf("/read?page=%d", page-1)
		if tag != "" {
			prevPage = fmt.Sprintf("%s&tag=%s", prevPage, tag)
		}
	}

	tags, err := dbpool.FindPopularTags(cfg.Space)
	if err != nil {
		logger.Error(err)
	}

	data := ReadPageData{
		Site:      *cfg.GetSiteData(),
		NextPage:  nextPage,
		PrevPage:  prevPage,
		Tags:      tags,
		HasFilter: tag != "",
	}
	for _, post := range pager.Data {
		item := PostItemData{
			URL:            template.URL(cfg.PostURL(post.Username, post.Slug)),
			BlogURL:        template.URL(cfg.BlogURL(post.Username)),
			Title:          shared.FilenameToTitle(post.Filename, post.Title),
			Description:    post.Description,
			Username:       post.Username,
			PublishAt:      post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: shared.TimeAgo(post.UpdatedAt),
			UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
		}
		data.Posts = append(data.Posts, item)
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
	posts, err := getPostsForUser(r, user, tag, 10)
	if err != nil {
		logger.Error(err)
		http.Error(w, "could not fetch posts for blog", http.StatusInternalServerError)
		return
	}

	ts, err := template.New("rss.page.tmpl").Funcs(shared.FuncMap).ParseFiles(
		cfg.StaticPath("html/rss.page.tmpl"),
		cfg.StaticPath("html/list.partial.tmpl"),
	)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	headerTxt := &HeaderTxt{
		Title: GetBlogName(username),
	}
	header, err := dbpool.FindPostWithFilename("_header.txt", user.ID, cfg.Space)
	if err == nil {
		linkify := imgs.NewImgsLinkify(username)
		parsedText := ParseText(header.Text, linkify)
		if parsedText.MetaData.Title != "" {
			headerTxt.Title = parsedText.MetaData.Title
		}

		if parsedText.MetaData.Description != "" {
			headerTxt.Bio = parsedText.MetaData.Description
		}
	}

	feed := &feeds.Feed{
		Title:       headerTxt.Title,
		Link:        &feeds.Link{Href: cfg.BlogURL(username)},
		Description: headerTxt.Bio,
		Author:      &feeds.Author{Name: username},
		Created:     time.Now(),
	}

	var feedItems []*feeds.Item
	for _, post := range posts {
		if slices.Contains(cfg.HiddenPosts, post.Filename) {
			continue
		}
		linkify := imgs.NewImgsLinkify(username)
		parsed := ParseText(post.Text, linkify)
		var tpl bytes.Buffer
		data := &PostPageData{
			ListType: parsed.MetaData.ListType,
			Items:    parsed.Items,
		}
		if err := ts.Execute(&tpl, data); err != nil {
			logger.Error(err)
			continue
		}

		item := &feeds.Item{
			Id:          cfg.PostURL(post.Username, post.Slug),
			Title:       shared.FilenameToTitle(post.Filename, post.Title),
			Link:        &feeds.Link{Href: cfg.PostURL(post.Username, post.Slug)},
			Content:     tpl.String(),
			Created:     *post.PublishAt,
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
		logger.Error(err)
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

	ts, err := template.New("rss.page.tmpl").Funcs(shared.FuncMap).ParseFiles(
		cfg.StaticPath("html/rss.page.tmpl"),
		cfg.StaticPath("html/list.partial.tmpl"),
	)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	feed := &feeds.Feed{
		Title:       fmt.Sprintf("%s discovery feed", cfg.Domain),
		Link:        &feeds.Link{Href: cfg.ReadURL()},
		Description: fmt.Sprintf("%s latest posts", cfg.Domain),
		Author:      &feeds.Author{Name: cfg.Domain},
		Created:     time.Now(),
	}

	var feedItems []*feeds.Item
	for _, post := range pager.Data {
		linkify := imgs.NewImgsLinkify(post.Username)
		parsed := ParseText(post.Text, linkify)
		var tpl bytes.Buffer
		data := &PostPageData{
			ListType: parsed.MetaData.ListType,
			Items:    parsed.Items,
		}
		if err := ts.Execute(&tpl, data); err != nil {
			logger.Error(err)
			continue
		}

		item := &feeds.Item{
			Id:          cfg.PostURL(post.Username, post.Slug),
			Title:       post.Title,
			Link:        &feeds.Link{Href: cfg.PostURL(post.Username, post.Slug)},
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
		logger.Error(err)
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
		shared.NewRoute("GET", "/spec", shared.CreatePageHandler("html/spec.page.tmpl")),
		shared.NewRoute("GET", "/ops", shared.CreatePageHandler("html/ops.page.tmpl")),
		shared.NewRoute("GET", "/privacy", shared.CreatePageHandler("html/privacy.page.tmpl")),
		shared.NewRoute("GET", "/help", shared.CreatePageHandler("html/help.page.tmpl")),
		shared.NewRoute("GET", "/transparency", transparencyHandler),
		shared.NewRoute("GET", "/read", readHandler),
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
		shared.NewRoute("GET", "/([^/]+)/([^/]+)", postHandler),
		shared.NewRoute("GET", "/raw/([^/]+)/([^/]+)", postRawHandler),
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
		shared.NewRoute("GET", "/([^/]+)", postHandler),
		shared.NewRoute("GET", "/raw/([^/]+)", postRawHandler),
	)

	return routes
}

func StartApiServer() {
	cfg := NewConfigSite()
	db := postgres.NewDB(&cfg.ConfigCms)
	defer db.Close()
	logger := cfg.Logger

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
