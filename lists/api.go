package internal

import (
	"bytes"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"git.sr.ht/~erock/lists.sh/pkg"
	"git.sr.ht/~erock/wish/cms/db"
	"git.sr.ht/~erock/wish/cms/db/postgres"
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
	ListType     string
	Items        []*pkg.ListItem
	PublishAtISO string
	PublishAt    string
}

type TransparencyPageData struct {
	Site      SitePageData
	Analytics *db.Analytics
}

func isRequestTrackable(r *http.Request) bool {
	return true
}

func renderTemplate(templates []string) (*template.Template, error) {
	files := make([]string, len(templates))
	copy(files, templates)
	files = append(
		files,
		"./html/footer.partial.tmpl",
		"./html/marketing-footer.partial.tmpl",
		"./html/base.layout.tmpl",
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
		ts, err := renderTemplate([]string{fname})

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

type HeaderTxt struct {
	Title    string
	Bio      string
	Nav      []*pkg.ListItem
	HasItems bool
}

type ReadmeTxt struct {
	HasItems bool
	ListType string
	Items    []*pkg.ListItem
}

func GetUsernameFromRequest(r *http.Request) string {
	subdomain := GetSubdomain(r)
	cfg := GetCfg(r)

	if !cfg.IsSubdomains() || subdomain == "" {
		return GetField(r, 0)
	}
	return subdomain
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
	posts, err := dbpool.FindUpdatedPostsForUser(user.ID, cfg.Space)
	if err != nil {
		logger.Error(err)
		http.Error(w, "could not fetch posts for blog", http.StatusInternalServerError)
		return
	}

	ts, err := renderTemplate([]string{
		"./html/blog.page.tmpl",
		"./html/list.partial.tmpl",
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

	postCollection := make([]PostItemData, 0, len(posts))
	for _, post := range posts {
		if post.Filename == "_header" {
			parsedText := pkg.ParseText(post.Text)
			if parsedText.MetaData.Title != "" {
				headerTxt.Title = parsedText.MetaData.Title
			}

			if parsedText.MetaData.Description != "" {
				headerTxt.Bio = parsedText.MetaData.Description
			}

			headerTxt.Nav = parsedText.Items
			if len(headerTxt.Nav) > 0 {
				headerTxt.HasItems = true
			}
		} else if post.Filename == "_readme" {
			parsedText := pkg.ParseText(post.Text)
			readmeTxt.Items = parsedText.Items
			readmeTxt.ListType = parsedText.MetaData.ListType
			if len(readmeTxt.Items) > 0 {
				readmeTxt.HasItems = true
			}
		} else {
			p := PostItemData{
				URL:            template.URL(cfg.PostURL(post.Username, post.Filename)),
				BlogURL:        template.URL(cfg.BlogURL(post.Username)),
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
		URL:       template.URL(cfg.BlogURL(username)),
		RSSURL:    template.URL(cfg.RssBlogURL(username)),
		Readme:    readmeTxt,
		Header:    headerTxt,
		Username:  username,
		Posts:     postCollection,
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

	header, _ := dbpool.FindPostWithFilename("_header", user.ID, cfg.Space)
	blogName := GetBlogName(username)
	if header != nil {
		headerParsed := pkg.ParseText(header.Text)
		if headerParsed.MetaData.Title != "" {
			blogName = headerParsed.MetaData.Title
		}
	}

	var data PostPageData
	post, err := dbpool.FindPostWithFilename(filename, user.ID, cfg.Space)
	if err == nil {
		parsedText := pkg.ParseText(post.Text)

		// we need the blog name from the readme unfortunately
		readme, err := dbpool.FindPostWithFilename("_readme", user.ID, cfg.Space)
		if err == nil {
			readmeParsed := pkg.ParseText(readme.Text)
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
			URL:          template.URL(cfg.PostURL(post.Username, post.Filename)),
			BlogURL:      template.URL(cfg.BlogURL(username)),
			Description:  post.Description,
			ListType:     parsedText.MetaData.ListType,
			Title:        FilenameToTitle(post.Filename, post.Title),
			PublishAt:    post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO: post.PublishAt.Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Items:        parsedText.Items,
		}
	} else {
		logger.Infof("post not found %s/%s", username, filename)
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
			Items: []*pkg.ListItem{
				{
					Value:  "oops!  we can't seem to find this post.",
					IsText: true,
				},
			},
		}
	}

	ts, err := renderTemplate([]string{
		"./html/post.page.tmpl",
		"./html/list.partial.tmpl",
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
		"./html/transparency.page.tmpl",
		"./html/footer.partial.tmpl",
		"./html/marketing-footer.partial.tmpl",
		"./html/base.layout.tmpl",
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
	dbpool := GetDB(r)
	logger := GetLogger(r)
	cfg := GetCfg(r)

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pager, err := dbpool.FindAllUpdatedPosts(&db.Pager{Num: 30, Page: page}, cfg.Space)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := renderTemplate([]string{
		"./html/read.page.tmpl",
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
			URL:            template.URL(cfg.PostURL(post.Username, post.Filename)),
			BlogURL:        template.URL(cfg.BlogURL(post.Username)),
			Title:          FilenameToTitle(post.Filename, post.Title),
			Description:    post.Description,
			Username:       post.Username,
			PublishAt:      post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: TimeAgo(post.UpdatedAt),
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
	posts, err := dbpool.FindUpdatedPostsForUser(user.ID, cfg.Space)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := template.ParseFiles("./html/rss.page.tmpl", "./html/list.partial.tmpl")
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	headerTxt := &HeaderTxt{
		Title: GetBlogName(username),
	}

	for _, post := range posts {
		if post.Filename == "_header" {
			parsedText := pkg.ParseText(post.Text)
			if parsedText.MetaData.Title != "" {
				headerTxt.Title = parsedText.MetaData.Title
			}

			if parsedText.MetaData.Description != "" {
				headerTxt.Bio = parsedText.MetaData.Description
			}

			break
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
		if slices.Contains(HiddenPosts, post.Filename) {
			continue
		}

		parsed := pkg.ParseText(post.Text)
		var tpl bytes.Buffer
		data := &PostPageData{
			ListType: parsed.MetaData.ListType,
			Items:    parsed.Items,
		}
		if err := ts.Execute(&tpl, data); err != nil {
			continue
		}

		item := &feeds.Item{
			Id:      cfg.PostURL(post.Username, post.Filename),
			Title:   FilenameToTitle(post.Filename, post.Title),
			Link:    &feeds.Link{Href: cfg.PostURL(post.Username, post.Filename)},
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
	dbpool := GetDB(r)
	logger := GetLogger(r)
	cfg := GetCfg(r)

	pager, err := dbpool.FindAllPosts(&db.Pager{Num: 25, Page: 0}, cfg.Space)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := template.ParseFiles("./html/rss.page.tmpl", "./html/list.partial.tmpl")
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
		parsed := pkg.ParseText(post.Text)
		var tpl bytes.Buffer
		data := &PostPageData{
			ListType: parsed.MetaData.ListType,
			Items:    parsed.Items,
		}
		if err := ts.Execute(&tpl, data); err != nil {
			continue
		}

		item := &feeds.Item{
			Id:      cfg.PostURL(post.Username, post.Filename),
			Title:   post.Title,
			Link:    &feeds.Link{Href: cfg.PostURL(post.Username, post.Filename)},
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
		logger.Error(err)
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

		contents, err := ioutil.ReadFile(fmt.Sprintf("./public/%s", file))
		if err != nil {
			logger.Error(err)
			http.Error(w, "file not found", 404)
		}

		w.Header().Add("Content-Type", contentType)

		_, err = w.Write(contents)
		if err != nil {
			logger.Error(err)
		}
	}
}

func createStaticRoutes() []Route {
	return []Route{
		NewRoute("GET", "/main.css", serveFile("main.css", "text/css")),
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
		NewRoute("GET", "/", createPageHandler("./html/marketing.page.tmpl")),
		NewRoute("GET", "/spec", createPageHandler("./html/spec.page.tmpl")),
		NewRoute("GET", "/ops", createPageHandler("./html/ops.page.tmpl")),
		NewRoute("GET", "/privacy", createPageHandler("./html/privacy.page.tmpl")),
		NewRoute("GET", "/help", createPageHandler("./html/help.page.tmpl")),
		NewRoute("GET", "/transparency", transparencyHandler),
		NewRoute("GET", "/read", readHandler),
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
		NewRoute("GET", "/([^/]+)/([^/]+)", postHandler),
	)

	return routes
}

func createSubdomainRoutes(staticRoutes []Route) []Route {
	routes := []Route{
		NewRoute("GET", "/", blogHandler),
		NewRoute("GET", "/rss", rssBlogHandler),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		NewRoute("GET", "/([^/]+)", postHandler),
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
