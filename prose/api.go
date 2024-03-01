package prose

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"slices"

	"github.com/gorilla/feeds"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/imgs"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
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
	BlogName     string
	Slug         string
	Title        string
	Description  string
	Username     string
	Contents     template.HTML
	PublishAtISO string
	PublishAt    string
	HasCSS       bool
	CssURL       template.URL
	Tags         []string
	Image        template.URL
	ImageCard    string
	Footer       template.HTML
	Favicon      template.URL
	Unlisted     bool
}

type TransparencyPageData struct {
	Site      shared.SitePageData
	Analytics *db.Analytics
}

type HeaderTxt struct {
	Title     string
	Bio       string
	Nav       []shared.Link
	HasLinks  bool
	Layout    string
	Image     template.URL
	ImageCard string
	Favicon   template.URL
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
		logger.Info("blog not found", "user", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}
	styles, err := dbpool.FindPostWithFilename("_styles.css", user.ID, cfg.Space)
	if err != nil {
		logger.Info("css not found", "user", username)
		http.Error(w, "css not found", http.StatusNotFound)
		return
	}

	w.Header().Add("Content-Type", "text/css")

	_, err = w.Write([]byte(styles.Text))
	if err != nil {
		logger.Error(err.Error())
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
		logger.Info("blog not found", "user", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	tag := r.URL.Query().Get("tag")
	pager := &db.Pager{Num: 1000, Page: 0}
	var posts []*db.Post
	var p *db.Paginate[*db.Post]
	if tag == "" {
		p, err = dbpool.FindPostsForUser(pager, user.ID, cfg.Space)
	} else {
		p, err = dbpool.FindUserPostsByTag(pager, tag, user.ID, cfg.Space)
	}
	posts = p.Data

	if err != nil {
		logger.Error(err.Error())
		http.Error(w, "could not fetch posts for blog", http.StatusInternalServerError)
		return
	}

	ts, err := shared.RenderTemplate(cfg, []string{
		cfg.StaticPath("html/blog-default.partial.tmpl"),
		cfg.StaticPath("html/blog-aside.partial.tmpl"),
		cfg.StaticPath("html/blog.page.tmpl"),
	})

	curl := shared.CreateURLFromRequest(cfg, r)

	if err != nil {
		logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	headerTxt := &HeaderTxt{
		Title:     GetBlogName(username),
		Bio:       "",
		Layout:    "default",
		ImageCard: "summary",
	}
	readmeTxt := &ReadmeTxt{}

	readme, err := dbpool.FindPostWithFilename("_readme.md", user.ID, cfg.Space)
	if err == nil {
		parsedText, err := shared.ParseText(readme.Text)
		if err != nil {
			logger.Error(err.Error())
		}
		headerTxt.Bio = parsedText.Description
		headerTxt.Layout = parsedText.Layout
		headerTxt.Image = template.URL(parsedText.Image)
		headerTxt.ImageCard = parsedText.ImageCard
		headerTxt.Favicon = template.URL(parsedText.Favicon)
		if parsedText.Title != "" {
			headerTxt.Title = parsedText.Title
		}

		headerTxt.Nav = []shared.Link{}
		for _, nav := range parsedText.Nav {
			u, _ := url.Parse(nav.URL)
			finURL := nav.URL
			if !u.IsAbs() {
				finURL = cfg.FullPostURL(
					curl,
					readme.Username,
					nav.URL,
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
	}

	hasCSS := false
	_, err = dbpool.FindPostWithFilename("_styles.css", user.ID, cfg.Space)
	if err == nil {
		hasCSS = true
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
		HasCSS:    hasCSS,
		CssURL:    template.URL(cfg.CssURL(username)),
		HasFilter: tag != "",
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err.Error())
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
	slug = strings.TrimSuffix(slug, "/")

	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Info("blog not found", "user", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	post, err := dbpool.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err != nil {
		logger.Info("post not found")
		http.Error(w, "post not found", http.StatusNotFound)
		return
	}

	w.Header().Add("Content-Type", "text/plain")

	_, err = w.Write([]byte(post.Text))
	if err != nil {
		logger.Error(err.Error())
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
	slug = strings.TrimSuffix(slug, "/")

	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Info("blog not found", "user", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	blogName := GetBlogName(username)
	curl := shared.CreateURLFromRequest(cfg, r)

	favicon := ""
	ogImage := ""
	ogImageCard := ""
	hasCSS := false
	var data PostPageData
	post, err := dbpool.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err == nil {
		parsedText, err := shared.ParseText(post.Text)
		if err != nil {
			logger.Error(err.Error())
		}

		// we need the blog name from the readme unfortunately
		readme, err := dbpool.FindPostWithFilename("_readme.md", user.ID, cfg.Space)
		if err == nil {
			readmeParsed, err := shared.ParseText(readme.Text)
			if err != nil {
				logger.Error(err.Error())
			}
			if readmeParsed.MetaData.Title != "" {
				blogName = readmeParsed.MetaData.Title
			}
			ogImage = readmeParsed.Image
			ogImageCard = readmeParsed.ImageCard
			favicon = readmeParsed.Favicon
		}

		if parsedText.Image != "" {
			ogImage = parsedText.Image
		}

		if parsedText.ImageCard != "" {
			ogImageCard = parsedText.ImageCard
		}

		css, err := dbpool.FindPostWithFilename("_styles.css", user.ID, cfg.Space)
		if err == nil {
			if len(css.Text) > 0 {
				hasCSS = true
			}
		}

		footer, err := dbpool.FindPostWithFilename("_footer.md", user.ID, cfg.Space)
		var footerHTML template.HTML
		if err == nil {
			footerParsed, err := shared.ParseText(footer.Text)
			if err != nil {
				logger.Error(err.Error())
			}
			footerHTML = template.HTML(footerParsed.Html)
		}

		// validate and fire off analytic event
		if isRequestTrackable(r) {
			_, err := dbpool.AddViewCount(post.ID)
			if err != nil {
				logger.Error(err.Error())
			}
		}

		unlisted := false
		if post.Hidden || post.PublishAt.After(time.Now()) {
			unlisted = true
		}

		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			PageTitle:    GetPostTitle(post),
			URL:          template.URL(cfg.FullPostURL(curl, post.Username, post.Slug)),
			BlogURL:      template.URL(cfg.FullBlogURL(curl, username)),
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
			Image:        template.URL(ogImage),
			ImageCard:    ogImageCard,
			Favicon:      template.URL(favicon),
			Footer:       footerHTML,
			Unlisted:     unlisted,
		}
	} else {
		// TODO: HACK to support imgs slugs inside prose
		// We definitely want to kill this feature in time
		imgPost, err := imgs.FindImgPost(r, user, slug)
		if err == nil && imgPost != nil {
			imgs.ImgRequest(w, r)
			return
		}

		data = PostPageData{
			Site:         *cfg.GetSiteData(),
			BlogURL:      template.URL(cfg.FullBlogURL(curl, username)),
			PageTitle:    "Post not found",
			Description:  "Post not found",
			Title:        "Post not found",
			PublishAt:    time.Now().Format("02 Jan, 2006"),
			PublishAtISO: time.Now().Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Contents:     "Oops!  we can't seem to find this post.",
			Unlisted:     true,
		}
		logger.Info("post not found", "user", username, "slug", slug)
	}

	ts, err := shared.RenderTemplate(cfg, []string{
		cfg.StaticPath("html/post.page.tmpl"),
	})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err.Error())
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
		logger.Error(err.Error())
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
		logger.Error(err.Error())
	}

	data := ReadPageData{
		Site:      *cfg.GetSiteData(),
		NextPage:  nextPage,
		PrevPage:  prevPage,
		Tags:      tags,
		HasFilter: tag != "",
	}

	curl := shared.NewCreateURL(cfg)
	for _, post := range pager.Data {
		item := PostItemData{
			URL:            template.URL(cfg.FullPostURL(curl, post.Username, post.Slug)),
			BlogURL:        template.URL(cfg.FullBlogURL(curl, post.Username)),
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
		logger.Error(err.Error())
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
		logger.Info("rss feed not found", "user", username)
		http.Error(w, "rss feed not found", http.StatusNotFound)
		return
	}

	tag := r.URL.Query().Get("tag")
	pager := &db.Pager{Num: 10, Page: 0}
	var posts []*db.Post
	var p *db.Paginate[*db.Post]
	if tag == "" {
		p, err = dbpool.FindPostsForUser(pager, user.ID, cfg.Space)
	} else {
		p, err = dbpool.FindUserPostsByTag(pager, tag, user.ID, cfg.Space)
	}
	posts = p.Data

	if err != nil {
		logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := template.ParseFiles(cfg.StaticPath("html/rss.page.tmpl"))
	if err != nil {
		logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	headerTxt := &HeaderTxt{
		Title: GetBlogName(username),
	}

	readme, err := dbpool.FindPostWithFilename("_readme.md", user.ID, cfg.Space)
	if err == nil {
		parsedText, err := shared.ParseText(readme.Text)
		if err != nil {
			logger.Error(err.Error())
		}
		if parsedText.Title != "" {
			headerTxt.Title = parsedText.Title
		}

		if parsedText.Description != "" {
			headerTxt.Bio = parsedText.Description
		}
	}

	curl := shared.CreateURLFromRequest(cfg, r)
	blogUrl := cfg.FullBlogURL(curl, username)

	feed := &feeds.Feed{
		Id:          blogUrl,
		Title:       headerTxt.Title,
		Link:        &feeds.Link{Href: fmt.Sprintf("%s/rss", blogUrl)},
		Description: headerTxt.Bio,
		Author:      &feeds.Author{Name: username},
		Created:     *user.CreatedAt,
	}

	var feedItems []*feeds.Item
	for _, post := range posts {
		if slices.Contains(cfg.HiddenPosts, post.Filename) {
			continue
		}
		parsed, err := shared.ParseText(post.Text)
		if err != nil {
			logger.Error(err.Error())
		}

		footer, err := dbpool.FindPostWithFilename("_footer.md", user.ID, cfg.Space)
		var footerHTML string
		if err == nil {
			footerParsed, err := shared.ParseText(footer.Text)
			if err != nil {
				logger.Error(err.Error())
			}
			footerHTML = footerParsed.Html
		}

		var tpl bytes.Buffer
		data := &PostPageData{
			Contents: template.HTML(parsed.Html + footerHTML),
		}
		if err := ts.Execute(&tpl, data); err != nil {
			continue
		}

		realUrl := cfg.FullPostURL(curl, post.Username, post.Slug)

		item := &feeds.Item{
			Id:          realUrl,
			Title:       shared.FilenameToTitle(post.Filename, post.Title),
			Link:        &feeds.Link{Href: realUrl},
			Content:     tpl.String(),
			Created:     *post.CreatedAt,
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
		logger.Error(err.Error())
		http.Error(w, "Could not generate atom rss feed", http.StatusInternalServerError)
	}

	w.Header().Add("Content-Type", "application/atom+xml")
	_, err = w.Write([]byte(rss))
	if err != nil {
		logger.Error(err.Error())
	}
}

func rssHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	cfg := shared.GetCfg(r)

	pager, err := dbpool.FindAllPosts(&db.Pager{Num: 25, Page: 0}, cfg.Space)
	if err != nil {
		logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := template.ParseFiles(cfg.StaticPath("html/rss.page.tmpl"))
	if err != nil {
		logger.Error(err.Error())
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

	curl := shared.CreateURLFromRequest(cfg, r)

	var feedItems []*feeds.Item
	for _, post := range pager.Data {
		parsed, err := shared.ParseText(post.Text)
		if err != nil {
			logger.Error(err.Error())
		}

		var tpl bytes.Buffer
		data := &PostPageData{
			Contents: template.HTML(parsed.Html),
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
		logger.Error(err.Error())
		http.Error(w, "Could not generate atom rss feed", http.StatusInternalServerError)
	}

	w.Header().Add("Content-Type", "application/atom+xml")
	_, err = w.Write([]byte(rss))
	if err != nil {
		logger.Error(err.Error())
	}
}

func serveFile(file string, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := shared.GetLogger(r)
		cfg := shared.GetCfg(r)

		contents, err := os.ReadFile(cfg.StaticPath(fmt.Sprintf("public/%s", file)))
		if err != nil {
			logger.Error(err.Error())
			http.Error(w, "file not found", 404)
		}
		w.Header().Add("Content-Type", contentType)

		_, err = w.Write(contents)
		if err != nil {
			logger.Error(err.Error())
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
		shared.NewRoute("GET", "/", readHandler),
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
		shared.NewRoute("GET", "/([^/]+)/atom", rssBlogHandler),
		shared.NewRoute("GET", "/([^/]+)/blog/index.xml", rssBlogHandler),
		shared.NewRoute("GET", "/([^/]+)/feed.xml", rssBlogHandler),
		shared.NewRoute("GET", "/([^/]+)/_styles.css", blogStyleHandler),
		shared.NewRoute("GET", "/raw/([^/]+)/(.+)", postRawHandler),
		shared.NewRoute("GET", "/([^/]+)/(.+)/(.+)", imgs.ImgRequest),
		shared.NewRoute("GET", "/([^/]+)/(.+).(?:jpg|jpeg|png|gif|webp|svg)$", imgs.ImgRequest),
		shared.NewRoute("GET", "/([^/]+)/i", imgs.ImgsListHandler),
		shared.NewRoute("GET", "/([^/]+)/(.+)", postHandler),
	)

	return routes
}

func createSubdomainRoutes(staticRoutes []shared.Route) []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "/", blogHandler),
		shared.NewRoute("GET", "/_styles.css", blogStyleHandler),
		shared.NewRoute("GET", "/rss", rssBlogHandler),
		shared.NewRoute("GET", "/rss.xml", rssBlogHandler),
		shared.NewRoute("GET", "/atom.xml", rssBlogHandler),
		shared.NewRoute("GET", "/feed.xml", rssBlogHandler),
		shared.NewRoute("GET", "/atom", rssBlogHandler),
		shared.NewRoute("GET", "/blog/index.xml", rssBlogHandler),
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	routes = append(
		routes,
		shared.NewRoute("GET", "/raw/(.+)", postRawHandler),
		shared.NewRoute("GET", "/([^/]+)/(.+)", imgs.ImgRequest),
		shared.NewRoute("GET", "/(.+).(?:jpg|jpeg|png|gif|webp|svg)$", imgs.ImgRequest),
		shared.NewRoute("GET", "/i", imgs.ImgsListHandler),
		shared.NewRoute("GET", "/(.+)", postHandler),
	)

	return routes
}

func StartApiServer() {
	cfg := NewConfigSite()
	db := postgres.NewDB(cfg.ConfigCms.DbURL, cfg.Logger)
	defer db.Close()
	logger := cfg.Logger

	var st storage.StorageServe
	var err error
	if cfg.MinioURL == "" {
		st, err = storage.NewStorageFS(cfg.StorageDir)
	} else {
		st, err = storage.NewStorageMinio(cfg.MinioURL, cfg.MinioUser, cfg.MinioPass)
	}

	if err != nil {
		logger.Error(err.Error())
	}

	staticRoutes := createStaticRoutes()

	if cfg.Debug {
		staticRoutes = shared.CreatePProfRoutes(staticRoutes)
	}

	mainRoutes := createMainRoutes(staticRoutes)
	subdomainRoutes := createSubdomainRoutes(staticRoutes)

	httpCtx := &shared.HttpCtx{
		Cfg:     cfg,
		Dbpool:  db,
		Storage: st,
		Logger:  logger,
	}
	handler := shared.CreateServe(mainRoutes, subdomainRoutes, httpCtx)
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
