package prose

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

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/db/postgres"
	"git.sr.ht/~erock/pico/imgs"
	"git.sr.ht/~erock/pico/shared"
	"github.com/gorilla/feeds"
	"golang.org/x/exp/slices"
)

type PageData struct {
	Site shared.SitePageData
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
	Site      shared.SitePageData
	PageTitle string
	URL       template.URL
	RSSURL    template.URL
	Username  string
	Readme    *ReadmeTxt
	Header    *HeaderTxt
	Posts     []PostItemData
	HasCSS    bool
	CssURL    template.URL
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
	Slug         string
	Title        string
	Description  string
	Username     string
	BlogName     string
	Contents     template.HTML
	PublishAtISO string
	PublishAt    string
	HasCSS       bool
	CssURL       template.URL
	Tags         []string
}

type TransparencyPageData struct {
	Site      shared.SitePageData
	Analytics *db.Analytics
}

type HeaderTxt struct {
	Title    string
	Bio      string
	Nav      []shared.Link
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
	return fmt.Sprintf("%s's blog", username)
}

func isRequestTrackable(r *http.Request) bool {
	return true
}

func blogStyleHandler(w http.ResponseWriter, r *http.Request) {
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
	styles, err := dbpool.FindPostWithFilename("_styles.css", user.ID, cfg.Space)
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

	hasCSS := false
	postCollection := make([]PostItemData, 0, len(posts))
	for _, post := range posts {
		if post.Filename == "_styles.css" && len(post.Text) > 0 {
			hasCSS = true
		} else if post.Filename == "_readme.md" {
			parsedText, err := shared.ParseText(post.Text, imgs.ImgBaseURL(post.Username))
			if err != nil {
				logger.Error(err)
			}
			headerTxt.Bio = parsedText.Description
			if parsedText.Title != "" {
				headerTxt.Title = parsedText.Title
			}

			headerTxt.Nav = []shared.Link{}
			for _, nav := range parsedText.Nav {
				u, _ := url.Parse(nav.URL)
				finURL := nav.URL
				if !u.IsAbs() {
					finURL = cfg.FullPostURL(
						post.Username,
						nav.URL,
						onSubdomain,
						withUserName,
					)
				}
				headerTxt.Nav = append(headerTxt.Nav, shared.Link{
					URL:  finURL,
					Text: nav.Text,
				})
			}

			readmeTxt.Contents = template.HTML(parsedText.Html)
			if len(readmeTxt.Contents) > 0 {
				readmeTxt.HasText = true
			}
		} else {
			p := PostItemData{
				URL:            template.URL(cfg.FullPostURL(post.Username, post.Slug, onSubdomain, withUserName)),
				BlogURL:        template.URL(cfg.FullBlogURL(post.Username, onSubdomain, withUserName)),
				Title:          shared.FilenameToTitle(post.Filename, post.Title),
				PublishAt:      post.PublishAt.Format("02 Jan, 2006"),
				PublishAtISO:   post.PublishAt.Format(time.RFC3339),
				UpdatedTimeAgo: shared.TimeAgo(post.UpdatedAt),
				UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
			}
			postCollection = append(postCollection, p)
		}
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
		HasCSS:    hasCSS,
		CssURL:    template.URL(cfg.CssURL(username)),
		HasFilter: tag != "",
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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

	blogName := GetBlogName(username)
	hostDomain := strings.Split(r.Host, ":")[0]
	appDomain := strings.Split(cfg.ConfigCms.Domain, ":")[0]

	onSubdomain := cfg.IsSubdomains() && strings.Contains(hostDomain, appDomain)
	withUserName := (!onSubdomain && hostDomain == appDomain) || !cfg.IsCustomdomains()

	hasCSS := false
	var data PostPageData
	post, err := dbpool.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err == nil {
		parsedText, err := shared.ParseText(post.Text, imgs.ImgBaseURL(username))
		if err != nil {
			logger.Error(err)
		}

		// we need the blog name from the readme unfortunately
		readme, err := dbpool.FindPostWithFilename("_readme.md", user.ID, cfg.Space)
		if err == nil {
			readmeParsed, err := shared.ParseText(readme.Text, imgs.ImgBaseURL(username))
			if err != nil {
				logger.Error(err)
			}
			if readmeParsed.MetaData.Title != "" {
				blogName = readmeParsed.MetaData.Title
			}
		}

		// we need the blog name from the readme unfortunately
		css, err := dbpool.FindPostWithFilename("_styles.css", user.ID, cfg.Space)
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
			URL:          template.URL(cfg.FullPostURL(post.Username, post.Slug, onSubdomain, withUserName)),
			BlogURL:      template.URL(cfg.FullBlogURL(username, onSubdomain, withUserName)),
			Description:  post.Description,
			Title:        shared.FilenameToTitle(post.Filename, post.Title),
			Slug:         post.Slug,
			PublishAt:    post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO: post.PublishAt.Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Contents:     template.HTML(parsedText.Html),
			HasCSS:       hasCSS,
			CssURL:       template.URL(cfg.CssURL(username)),
			Tags:         parsedText.Tags,
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

func readHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	cfg := shared.GetCfg(r)

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	tag := r.URL.Query().Get("tag")
	var pager *db.Paginate[*db.Post]
	var err error
	if tag == "" {
		pager, err = dbpool.FindAllPosts(&db.Pager{Num: 30, Page: page}, cfg.Space)
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
			URL:            template.URL(cfg.FullPostURL(post.Username, post.Slug, true, true)),
			BlogURL:        template.URL(cfg.FullBlogURL(post.Username, true, true)),
			Title:          shared.FilenameToTitle(post.Filename, post.Title),
			Description:    post.Description,
			Username:       post.Username,
			PublishAt:      post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: shared.TimeAgo(post.UpdatedAt),
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
	if tag == "" {
		posts, err = dbpool.FindPostsForUser(user.ID, cfg.Space)
	} else {
		posts, err = dbpool.FindUserPostsByTag(tag, user.ID, cfg.Space)
	}

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

	for _, post := range posts {
		if post.Filename == "_readme.md" {
			parsedText, err := shared.ParseText(post.Text, imgs.ImgBaseURL(post.Username))
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
		parsed, err := shared.ParseText(post.Text, imgs.ImgBaseURL(post.Username))
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

		realUrl := cfg.FullPostURL(post.Username, post.Slug, onSubdomain, withUserName)
		if !onSubdomain && !withUserName {
			realUrl = fmt.Sprintf("%s://%s%s", cfg.Protocol, r.Host, realUrl)
		}

		item := &feeds.Item{
			Id:          realUrl,
			Title:       shared.FilenameToTitle(post.Filename, post.Title),
			Link:        &feeds.Link{Href: realUrl},
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
		parsed, err := shared.ParseText(post.Text, imgs.ImgBaseURL(post.Username))
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

func serveFile(file string, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := shared.GetLogger(r)
		cfg := shared.GetCfg(r)

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

func createStaticRoutes() []shared.Route {
	return []shared.Route{
		shared.NewRoute("GET", "/main.css", serveFile("main.css", "text/css")),
		shared.NewRoute("GET", "/prose.css", serveFile("prose.css", "text/css")),
		shared.NewRoute("GET", "/syntax.css", serveFile("syntax.css", "text/css")),
		shared.NewRoute("GET", "/card.png", serveFile("card.png", "image/png")),
		shared.NewRoute("GET", "/favicon-16x16.png", serveFile("favicon-16x16.png", "image/png")),
		shared.NewRoute("GET", "/favicon-32x32.png", serveFile("favicon-32x32.png", "image/png")),
		shared.NewRoute("GET", "/apple-touch-icon.png", serveFile("apple-touch-icon.png", "image/png")),
		shared.NewRoute("GET", "/favicon.ico", serveFile("favicon.ico", "image/x-icon")),
		shared.NewRoute("GET", "/robots.txt", serveFile("robots.txt", "text/plain")),
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
		shared.NewRoute("GET", "/([^/]+)/styles.css", blogStyleHandler),
		shared.NewRoute("GET", "/([^/]+)/([^/]+)", postHandler),
		shared.NewRoute("GET", "/raw/([^/]+)/([^/]+)", postRawHandler),
	)

	return routes
}

func createSubdomainRoutes(staticRoutes []shared.Route) []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "/", blogHandler),
		shared.NewRoute("GET", "/_styles.css", blogStyleHandler),
		shared.NewRoute("GET", "/rss", rssBlogHandler),
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
