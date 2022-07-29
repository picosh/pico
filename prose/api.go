package internal

import (
	"bytes"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"git.sr.ht/~erock/pico/wish/cms/db"
	"git.sr.ht/~erock/pico/wish/cms/db/postgres"
	"github.com/gorilla/feeds"
	"golang.org/x/exp/slices"
)

type PageData struct {
	Site SitePageData
}

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
	Score          string
}

type BlogPageData struct {
	Site      SitePageData
	PageTitle string
	URL       template.URL
	RSSURL    template.URL
	Username  string
	Readme    *ReadmeTxt
	Header    *HeaderTxt
	Posts     []PostItemData
	HasCSS    bool
	CssURL    template.URL
}

type ReadPageData struct {
	Site     SitePageData
	NextPage string
	PrevPage string
	Posts    []PostItemData
}

type PostPageData struct {
	Site         SitePageData
	PageTitle    string
	URL          template.URL
	BlogURL      template.URL
	Title        string
	Description  string
	Username     string
	BlogName     string
	Contents     template.HTML
	PublishAtISO string
	PublishAt    string
	HasCSS       bool
	CssURL       template.URL
}

type TransparencyPageData struct {
	Site      SitePageData
	Analytics *db.Analytics
}

func isRequestTrackable(r *http.Request) bool {
	return true
}

func renderTemplate(cfg *ConfigSite, templates []string) (*template.Template, error) {
	files := make([]string, len(templates))
	copy(files, templates)
	files = append(
		files,
		cfg.StaticPath("html/footer.partial.tmpl"),
		cfg.StaticPath("html/marketing-footer.partial.tmpl"),
		cfg.StaticPath("html/base.layout.tmpl"),
	)

	ts, err := template.ParseFiles(files...)
	if err != nil {
		return nil, err
	}
	return ts, nil
}

func createPageHandler(fname string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := GetLogger(r)
		cfg := GetCfg(r)
		ts, err := renderTemplate(cfg, []string{cfg.StaticPath(fname)})

		if err != nil {
			logger.Error(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := PageData{
			Site: *cfg.GetSiteData(),
		}
		err = ts.Execute(w, data)
		if err != nil {
			logger.Error(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

type Link struct {
	URL  string
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

func GetUsernameFromRequest(r *http.Request) string {
	subdomain := GetSubdomain(r)
	cfg := GetCfg(r)

	if !cfg.IsSubdomains() || subdomain == "" {
		return GetField(r, 0)
	}
	return subdomain
}

func blogStyleHandler(w http.ResponseWriter, r *http.Request) {
	username := GetUsernameFromRequest(r)
	dbpool := GetDB(r)
	logger := GetLogger(r)
	cfg := GetCfg(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("blog not found: %s", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}
	styles, err := dbpool.FindPostWithFilename("_styles", user.ID, cfg.Space)
	if err != nil {
		logger.Infof("css not found for: %s", username)
		http.Error(w, "css not found", http.StatusNotFound)
		return
	}

	w.Header().Add("Content-Type", "text/css")

	_, err = w.Write([]byte(styles.Text))
	if err != nil {
		logger.Error(err)
		http.Error(w, "server error", 500)
	}
}

func blogHandler(w http.ResponseWriter, r *http.Request) {
	username := GetUsernameFromRequest(r)
	dbpool := GetDB(r)
	logger := GetLogger(r)
	cfg := GetCfg(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("blog not found: %s", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}
	posts, err := dbpool.FindPostsForUser(user.ID, cfg.Space)
	if err != nil {
		logger.Error(err)
		http.Error(w, "could not fetch posts for blog", http.StatusInternalServerError)
		return
	}

	hostDomain := strings.Split(r.Host, ":")[0]
	appDomain := strings.Split(cfg.ConfigCms.Domain, ":")[0]

	onSubdomain := cfg.IsSubdomains() && strings.Contains(hostDomain, appDomain)
	withUserName := (!onSubdomain && hostDomain == appDomain) || !cfg.IsCustomdomains()

	ts, err := renderTemplate(cfg, []string{
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

	hasCSS := false
	postCollection := make([]PostItemData, 0, len(posts))
	for _, post := range posts {
		if post.Filename == "_styles" && len(post.Text) > 0 {
			hasCSS = true
		} else if post.Filename == "_readme" {
			parsedText, err := ParseText(post.Text)
			if err != nil {
				logger.Error(err)
			}
			headerTxt.Bio = parsedText.Description
			if parsedText.Title != "" {
				headerTxt.Title = parsedText.Title
			}
			headerTxt.Nav = parsedText.Nav
			readmeTxt.Contents = template.HTML(parsedText.Html)
			if len(readmeTxt.Contents) > 0 {
				readmeTxt.HasText = true
			}
		} else {
			p := PostItemData{
				URL:            template.URL(cfg.FullPostURL(post.Username, post.Filename, onSubdomain, withUserName)),
				BlogURL:        template.URL(cfg.FullBlogURL(post.Username, onSubdomain, withUserName)),
				Title:          FilenameToTitle(post.Filename, post.Title),
				PublishAt:      post.PublishAt.Format("02 Jan, 2006"),
				PublishAtISO:   post.PublishAt.Format(time.RFC3339),
				UpdatedTimeAgo: TimeAgo(post.UpdatedAt),
				UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
			}
			postCollection = append(postCollection, p)
		}
	}

	data := BlogPageData{
		Site:      *cfg.GetSiteData(),
		PageTitle: headerTxt.Title,
		URL:       template.URL(cfg.FullBlogURL(username, onSubdomain, withUserName)),
		RSSURL:    template.URL(cfg.RssBlogURL(username, onSubdomain, withUserName)),
		Readme:    readmeTxt,
		Header:    headerTxt,
		Username:  username,
		Posts:     postCollection,
		HasCSS:    hasCSS,
		CssURL:    template.URL(cfg.CssURL(username)),
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
	return fmt.Sprintf("%s's blog", username)
}

func postRawHandler(w http.ResponseWriter, r *http.Request) {
	username := GetUsernameFromRequest(r)
	subdomain := GetSubdomain(r)
	cfg := GetCfg(r)

	var filename string
	if !cfg.IsSubdomains() || subdomain == "" {
		filename, _ = url.PathUnescape(GetField(r, 1))
	} else {
		filename, _ = url.PathUnescape(GetField(r, 0))
	}

	dbpool := GetDB(r)
	logger := GetLogger(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("blog not found: %s", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	post, err := dbpool.FindPostWithFilename(filename, user.ID, cfg.Space)
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
	username := GetUsernameFromRequest(r)
	subdomain := GetSubdomain(r)
	cfg := GetCfg(r)

	var filename string
	if !cfg.IsSubdomains() || subdomain == "" {
		filename, _ = url.PathUnescape(GetField(r, 1))
	} else {
		filename, _ = url.PathUnescape(GetField(r, 0))
	}

	dbpool := GetDB(r)
	logger := GetLogger(r)

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

	hasCSS := false
	var data PostPageData
	post, err := dbpool.FindPostWithFilename(filename, user.ID, cfg.Space)
	if err == nil {
		parsedText, err := ParseText(post.Text)
		if err != nil {
			logger.Error(err)
		}

		// we need the blog name from the readme unfortunately
		readme, err := dbpool.FindPostWithFilename("_readme", user.ID, cfg.Space)
		if err == nil {
			readmeParsed, err := ParseText(readme.Text)
			if err != nil {
				logger.Error(err)
			}
			if readmeParsed.MetaData.Title != "" {
				blogName = readmeParsed.MetaData.Title
			}
		}

		// we need the blog name from the readme unfortunately
		css, err := dbpool.FindPostWithFilename("_styles", user.ID, cfg.Space)
		if err == nil {
			if len(css.Text) > 0 {
				hasCSS = true
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
			URL:          template.URL(cfg.FullPostURL(post.Username, post.Filename, onSubdomain, withUserName)),
			BlogURL:      template.URL(cfg.FullBlogURL(username, onSubdomain, withUserName)),
			Description:  post.Description,
			Title:        FilenameToTitle(post.Filename, post.Title),
			PublishAt:    post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO: post.PublishAt.Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Contents:     template.HTML(parsedText.Html),
			HasCSS:       hasCSS,
			CssURL:       template.URL(cfg.CssURL(username)),
		}
	} else {
		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			BlogURL:      template.URL(cfg.FullBlogURL(username, onSubdomain, withUserName)),
			PageTitle:    "Post not found",
			Description:  "Post not found",
			Title:        "Post not found",
			PublishAt:    time.Now().Format("02 Jan, 2006"),
			PublishAtISO: time.Now().Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Contents:     "Oops!  we can't seem to find this post.",
		}
		logger.Infof("post not found %s/%s", username, filename)
	}

	ts, err := renderTemplate(cfg, []string{
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
	dbpool := GetDB(r)
	logger := GetLogger(r)
	cfg := GetCfg(r)

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

func checkHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := GetDB(r)
	cfg := GetCfg(r)

	if cfg.IsCustomdomains() {
		hostDomain := r.URL.Query().Get("domain")
		appDomain := strings.Split(cfg.ConfigCms.Domain, ":")[0]

		if !strings.Contains(hostDomain, appDomain) {
			subdomain := GetCustomDomain(hostDomain)
			if subdomain != "" {
				u, err := dbpool.FindUserForName(subdomain)
				if u != nil && err == nil {
					w.WriteHeader(http.StatusOK)
					return
				}
			}
		}
	}

	w.WriteHeader(http.StatusNotFound)
}

func readHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := GetDB(r)
	logger := GetLogger(r)
	cfg := GetCfg(r)

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pager, err := dbpool.FindAllPosts(&db.Pager{Num: 30, Page: page}, cfg.Space)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := renderTemplate(cfg, []string{
		cfg.StaticPath("html/read.page.tmpl"),
	})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	nextPage := ""
	if page < pager.Total-1 {
		nextPage = fmt.Sprintf("/read?page=%d", page+1)
	}

	prevPage := ""
	if page > 0 {
		prevPage = fmt.Sprintf("/read?page=%d", page-1)
	}

	data := ReadPageData{
		Site:     *cfg.GetSiteData(),
		NextPage: nextPage,
		PrevPage: prevPage,
	}
	for _, post := range pager.Data {
		item := PostItemData{
			URL:            template.URL(cfg.FullPostURL(post.Username, post.Filename, true, true)),
			BlogURL:        template.URL(cfg.FullBlogURL(post.Username, true, true)),
			Title:          FilenameToTitle(post.Filename, post.Title),
			Description:    post.Description,
			Username:       post.Username,
			PublishAt:      post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: TimeAgo(post.UpdatedAt),
			UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
			Score:          post.Score,
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
	username := GetUsernameFromRequest(r)
	dbpool := GetDB(r)
	logger := GetLogger(r)
	cfg := GetCfg(r)

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

	for _, post := range posts {
		if post.Filename == "_readme" {
			parsedText, err := ParseText(post.Text)
			if err != nil {
				logger.Error(err)
			}
			if parsedText.Title != "" {
				headerTxt.Title = parsedText.Title
			}

			if parsedText.Description != "" {
				headerTxt.Bio = parsedText.Description
			}

			break
		}
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
		if slices.Contains(hiddenPosts, post.Filename) {
			continue
		}
		parsed, err := ParseText(post.Text)
		if err != nil {
			logger.Error(err)
		}
		var tpl bytes.Buffer
		data := &PostPageData{
			Contents: template.HTML(parsed.Html),
		}
		if err := ts.Execute(&tpl, data); err != nil {
			continue
		}

		realUrl := cfg.FullPostURL(post.Username, post.Filename, onSubdomain, withUserName)
		if !onSubdomain && !withUserName {
			realUrl = fmt.Sprintf("%s://%s%s", cfg.Protocol, r.Host, realUrl)
		}

		item := &feeds.Item{
			Id:      realUrl,
			Title:   FilenameToTitle(post.Filename, post.Title),
			Link:    &feeds.Link{Href: realUrl},
			Content: tpl.String(),
			Created: *post.PublishAt,
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
	dbpool := GetDB(r)
	logger := GetLogger(r)
	cfg := GetCfg(r)

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
		Title:       fmt.Sprintf("%s discovery feed", cfg.Domain),
		Link:        &feeds.Link{Href: cfg.ReadURL()},
		Description: fmt.Sprintf("%s latest posts", cfg.Domain),
		Author:      &feeds.Author{Name: cfg.Domain},
		Created:     time.Now(),
	}

	hostDomain := strings.Split(r.Host, ":")[0]
	appDomain := strings.Split(cfg.ConfigCms.Domain, ":")[0]

	onSubdomain := cfg.IsSubdomains() && strings.Contains(hostDomain, appDomain)
	withUserName := (!onSubdomain && hostDomain == appDomain) || !cfg.IsCustomdomains()

	var feedItems []*feeds.Item
	for _, post := range pager.Data {
		parsed, err := ParseText(post.Text)
		if err != nil {
			logger.Error(err)
		}

		var tpl bytes.Buffer
		data := &PostPageData{
			Contents: template.HTML(parsed.Html),
		}
		if err := ts.Execute(&tpl, data); err != nil {
			continue
		}

		realUrl := cfg.FullPostURL(post.Username, post.Filename, onSubdomain, withUserName)
		if !onSubdomain && !withUserName {
			realUrl = fmt.Sprintf("%s://%s%s", cfg.Protocol, r.Host, realUrl)
		}

		item := &feeds.Item{
			Id:      realUrl,
			Title:   post.Title,
			Link:    &feeds.Link{Href: realUrl},
			Content: tpl.String(),
			Created: *post.PublishAt,
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

func serveFile(file string, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := GetLogger(r)
		cfg := GetCfg(r)

		contents, err := ioutil.ReadFile(cfg.StaticPath(fmt.Sprintf("public/%s", file)))
		if err != nil {
			logger.Error(err)
			http.Error(w, "file not found", 404)
		}
		w.Header().Add("Content-Type", contentType)

		_, err = w.Write(contents)
		if err != nil {
			logger.Error(err)
			http.Error(w, "server error", 500)
		}
	}
}

func createStaticRoutes() []Route {
	return []Route{
		NewRoute("GET", "/main.css", serveFile("main.css", "text/css")),
		NewRoute("GET", "/syntax.css", serveFile("syntax.css", "text/css")),
		NewRoute("GET", "/card.png", serveFile("card.png", "image/png")),
		NewRoute("GET", "/favicon-16x16.png", serveFile("favicon-16x16.png", "image/png")),
		NewRoute("GET", "/favicon-32x32.png", serveFile("favicon-32x32.png", "image/png")),
		NewRoute("GET", "/apple-touch-icon.png", serveFile("apple-touch-icon.png", "image/png")),
		NewRoute("GET", "/favicon.ico", serveFile("favicon.ico", "image/x-icon")),
		NewRoute("GET", "/robots.txt", serveFile("robots.txt", "text/plain")),
	}
}

func createMainRoutes(staticRoutes []Route) []Route {
	routes := []Route{
		NewRoute("GET", "/", createPageHandler("html/marketing.page.tmpl")),
		NewRoute("GET", "/spec", createPageHandler("html/spec.page.tmpl")),
		NewRoute("GET", "/ops", createPageHandler("html/ops.page.tmpl")),
		NewRoute("GET", "/privacy", createPageHandler("html/privacy.page.tmpl")),
		NewRoute("GET", "/help", createPageHandler("html/help.page.tmpl")),
		NewRoute("GET", "/transparency", transparencyHandler),
		NewRoute("GET", "/read", readHandler),
		NewRoute("GET", "/check", checkHandler),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		NewRoute("GET", "/rss", rssHandler),
		NewRoute("GET", "/rss.xml", rssHandler),
		NewRoute("GET", "/atom.xml", rssHandler),
		NewRoute("GET", "/feed.xml", rssHandler),

		NewRoute("GET", "/([^/]+)", blogHandler),
		NewRoute("GET", "/([^/]+)/rss", rssBlogHandler),
		NewRoute("GET", "/([^/]+)/styles.css", blogStyleHandler),
		NewRoute("GET", "/([^/]+)/([^/]+)", postHandler),
		NewRoute("GET", "/raw/([^/]+)/([^/]+)", postRawHandler),
	)

	return routes
}

func createSubdomainRoutes(staticRoutes []Route) []Route {
	routes := []Route{
		NewRoute("GET", "/", blogHandler),
		NewRoute("GET", "/_styles.css", blogStyleHandler),
		NewRoute("GET", "/rss", rssBlogHandler),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		NewRoute("GET", "/([^/]+)", postHandler),
		NewRoute("GET", "/raw/([^/]+)", postRawHandler),
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

	handler := CreateServe(mainRoutes, subdomainRoutes, cfg, db, logger)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Infof("Starting server on port %s", cfg.Port)
	logger.Infof("Subdomains enabled: %t", cfg.SubdomainsEnabled)
	logger.Infof("Domain: %s", cfg.Domain)
	logger.Infof("Email: %s", cfg.Email)

	logger.Fatal(http.ListenAndServe(portStr, router))
}
