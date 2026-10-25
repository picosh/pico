package prose

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"slices"

	"github.com/gorilla/feeds"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
	"github.com/picosh/pico/pkg/storage"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	Score          int
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
	Diff         template.HTML
	UpdatedAtISO string
	UpdatedAt    string
	List         *shared.ListParsedText
}

type HeaderTxt struct {
	Title     string
	Bio       string
	Nav       []shared.Link
	HasLinks  bool
	Image     template.URL
	ImageCard string
	Favicon   template.URL
	Domain    string
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

func (web *webRouter) blogStyleHandler(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	dbpool := web.db
	logger := web.cfg.Logger
	cfg := web.cfg

	user, err := dbpool.FindUserByName(username)
	if err != nil {
		logger.Info("blog not found", "user", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}
	router.SetUser(r, user.Name)
	logger = shared.LoggerWithUser(logger, user)

	styles, err := dbpool.FindPostWithFilename("_styles.css", user.ID, cfg.Space)
	if err != nil {
		logger.Info("css not found")
		http.Error(w, "css not found", http.StatusNotFound)
		return
	}

	w.Header().Add("Content-Type", "text/css")

	_, err = w.Write([]byte(styles.Text))
	if err != nil {
		logger.Error("write to response writer", "err", err.Error())
		http.Error(w, "server error", 500)
	}
}

func (web *webRouter) blogHandler(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	dbpool := web.db
	logger := web.cfg.Logger
	cfg := web.cfg

	user, err := dbpool.FindUserByName(username)
	if err != nil {
		logger.Info("blog not found", "user", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}
	router.SetUser(r, user.Name)
	logger = shared.LoggerWithUser(logger, user)

	tag := r.URL.Query().Get("tag")
	pager := &db.Pager{Num: 250, Page: 0}
	var posts []*db.Post
	var p *db.Paginate[*db.Post]
	if tag == "" {
		p, err = dbpool.FindPostsByUser(pager, user.ID, cfg.Space)
	} else {
		p, err = dbpool.FindUserPostsByTag(pager, tag, user.ID, cfg.Space)
	}
	posts = p.Data

	if err != nil {
		logger.Error("find posts", "err", err.Error())
		http.Error(w, "could not fetch posts for blog", http.StatusInternalServerError)
		return
	}

	ts, err := router.RenderTemplate(staticDir, []string{
		cfg.StaticPath("html/blog-default.partial.tmpl"),
		cfg.StaticPath("html/blog.page.tmpl"),
	})

	curl := shared.CreateURLFromRequest(cfg.Protocol, cfg.Domain, r)

	if err != nil {
		logger.Error("render template", "err", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	headerTxt := &HeaderTxt{
		Title:     GetBlogName(username),
		Bio:       "",
		ImageCard: "summary",
	}
	readmeTxt := &ReadmeTxt{}

	readme, err := dbpool.FindPostWithFilename("_readme.md", user.ID, cfg.Space)
	if err == nil {
		parsedText, err := shared.ParseText(readme.Text)
		if err != nil {
			logger.Error("readme", "err", err.Error())
		}
		headerTxt.Bio = parsedText.Description
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
				finURL = curl.FullPostURL(readme.Username,
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
			URL:            template.URL(curl.FullPostURL(post.Username, post.Slug)),
			BlogURL:        template.URL(curl.FullBlogURL(post.Username)),
			Title:          shared.FilenameToTitle(post.Filename, post.Title),
			PublishAt:      post.PublishAt.Format(time.DateOnly),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: shared.TimeAgo(post.UpdatedAt),
			UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
		}
		postCollection = append(postCollection, p)
	}

	data := BlogPageData{
		Site:      cfg.SiteData(),
		PageTitle: headerTxt.Title,
		URL:       template.URL(curl.FullBlogURL(username)),
		RSSURL:    template.URL(curl.RssBlogURL(username, tag)),
		Readme:    readmeTxt,
		Header:    headerTxt,
		Username:  username,
		Posts:     postCollection,
		HasCSS:    hasCSS,
		CssURL:    template.URL(cfg.CssURL()),
		HasFilter: tag != "",
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error("template execute", "err", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (web *webRouter) postRawHandler(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	cfg := web.cfg
	slug, _ := url.PathUnescape(r.PathValue("slug"))
	slug = strings.TrimSuffix(slug, "/")

	dbpool := web.db
	logger := web.cfg.Logger
	logger = logger.With("slug", slug)

	user, err := dbpool.FindUserByName(username)
	if err != nil {
		logger.Info("blog not found", "user", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}
	router.SetUser(r, user.Name)
	logger = shared.LoggerWithUser(logger, user)

	post, err := dbpool.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err != nil {
		logger.Info("post not found")
		http.Error(w, "post not found", http.StatusNotFound)
		return
	}

	w.Header().Add("Content-Type", "text/plain")

	_, err = w.Write([]byte(post.Text))
	if err != nil {
		logger.Error("write to response writer", "err", err.Error())
		http.Error(w, "server error", 500)
	}
}

func (web *webRouter) robotsHandler(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	cfg := web.cfg
	dbpool := web.db
	logger := web.cfg.Logger
	user, err := dbpool.FindUserByName(username)
	if err != nil {
		logger.Info("blog not found", "user", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}
	router.SetUser(r, user.Name)
	logger = shared.LoggerWithUser(logger, user)
	w.Header().Add("Content-Type", "text/plain")

	post, err := dbpool.FindPostWithFilename("robots.txt", user.ID, cfg.Space)
	txt := ""
	if err == nil {
		txt = post.Text
	}
	_, err = w.Write([]byte(txt))
	if err != nil {
		logger.Error("write to response writer", "err", err)
		http.Error(w, "server error", 500)
	}
}

func (web *webRouter) postHandler(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	cfg := web.cfg
	slug, _ := url.PathUnescape(r.PathValue("slug"))
	slug = strings.TrimSuffix(slug, "/")

	dbpool := web.db
	logger := web.cfg.Logger

	user, err := dbpool.FindUserByName(username)
	if err != nil {
		logger.Info("blog not found", "user", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}
	router.SetUser(r, user.Name)

	logger = shared.LoggerWithUser(logger, user)
	logger = logger.With("slug", slug)

	blogName := GetBlogName(username)
	curl := shared.CreateURLFromRequest(cfg.Protocol, cfg.Domain, r)

	favicon := ""
	ogImage := ""
	ogImageCard := ""
	hasCSS := false
	var data PostPageData

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
			logger.Error("footer", "err", err.Error())
		}
		footerHTML = template.HTML(footerParsed.Html)
	}

	// we need the blog name from the readme unfortunately
	readme, err := dbpool.FindPostWithFilename("_readme.md", user.ID, cfg.Space)
	if err == nil {
		readmeParsed, err := shared.ParseText(readme.Text)
		if err != nil {
			logger.Error("readme", "err", err.Error())
		}
		if readmeParsed.Title != "" {
			blogName = readmeParsed.Title
		}
		ogImage = readmeParsed.Image
		ogImageCard = readmeParsed.ImageCard
		favicon = readmeParsed.Favicon
	}

	diff := ""
	post, err := dbpool.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err == nil {
		logger.Info("post found", "id", post.ID, "filename", post.FileSize)
		ext := filepath.Ext(post.Filename)
		contents := template.HTML("")
		tags := []string{}
		unlisted := false
		var list *shared.ListParsedText

		switch ext {
		case ".lxt":
			list = shared.ListParseText(post.Text)

			tags = list.Tags
			if list.Image != "" {
				ogImage = list.Image
			}
			if list.ImageCard != "" {
				ogImageCard = list.ImageCard
			}
			if post.Hidden || post.PublishAt.After(time.Now()) {
				unlisted = true
			}
		case ".md":
			parsedText, err := shared.ParseText(post.Text)
			if err != nil {
				logger.Error("could not parse md text", "err", err.Error())
			}

			tags = parsedText.Tags
			if parsedText.Image != "" {
				ogImage = parsedText.Image
			}
			if parsedText.ImageCard != "" {
				ogImageCard = parsedText.ImageCard
			}
			if post.Hidden || post.PublishAt.After(time.Now()) {
				unlisted = true
			}
			contents = template.HTML(parsedText.Html)
		}

		data = PostPageData{
			Site:         cfg.SiteData(),
			PageTitle:    GetPostTitle(post),
			URL:          template.URL(curl.FullPostURL(post.Username, post.Slug)),
			BlogURL:      template.URL(curl.FullBlogURL(username)),
			Description:  post.Description,
			Title:        shared.FilenameToTitle(post.Filename, post.Title),
			Slug:         post.Slug,
			PublishAt:    post.PublishAt.Format(time.DateOnly),
			PublishAtISO: post.PublishAt.Format(time.RFC3339),
			UpdatedAt:    post.UpdatedAt.Format(time.DateOnly),
			UpdatedAtISO: post.UpdatedAt.Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			Contents:     contents,
			HasCSS:       hasCSS,
			CssURL:       template.URL(cfg.CssURL()),
			Tags:         tags,
			Image:        template.URL(ogImage),
			ImageCard:    ogImageCard,
			Favicon:      template.URL(favicon),
			Footer:       footerHTML,
			Unlisted:     unlisted,
			Diff:         template.HTML(diff),
			List:         list,
		}
	} else {
		logger.Info("post not found")
		notFound, err := dbpool.FindPostWithFilename("_404.md", user.ID, cfg.Space)
		contents := template.HTML("Oops!  we can't seem to find this post.")
		title := "Post not found"
		desc := "Post not found"
		if err == nil {
			notFoundParsed, err := shared.ParseText(notFound.Text)
			if err != nil {
				logger.Error("parse not found file", "err", err.Error())
			}
			if notFoundParsed.Title != "" {
				title = notFoundParsed.Title
			}
			if notFoundParsed.Description != "" {
				desc = notFoundParsed.Description
			}
			ogImage = notFoundParsed.Image
			ogImageCard = notFoundParsed.ImageCard
			favicon = notFoundParsed.Favicon
			contents = template.HTML(notFoundParsed.Html)
		}

		now := time.Now()

		data = PostPageData{
			Site:         cfg.SiteData(),
			BlogURL:      template.URL(curl.FullBlogURL(username)),
			PageTitle:    title,
			Description:  desc,
			Title:        title,
			PublishAt:    now.Format(time.DateOnly),
			PublishAtISO: now.Format(time.RFC3339),
			UpdatedAt:    now.Format(time.DateOnly),
			UpdatedAtISO: now.Format(time.RFC3339),
			Username:     username,
			BlogName:     blogName,
			HasCSS:       hasCSS,
			CssURL:       template.URL(cfg.CssURL()),
			Image:        template.URL(ogImage),
			ImageCard:    ogImageCard,
			Favicon:      template.URL(favicon),
			Footer:       footerHTML,
			Contents:     contents,
			Unlisted:     true,
		}
		w.WriteHeader(http.StatusNotFound)
	}

	ts, err := router.RenderTemplate(staticDir, []string{
		cfg.StaticPath("html/list.partial.tmpl"),
		cfg.StaticPath("html/post.page.tmpl"),
	})

	if err != nil {
		logger.Error("render template", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	logger.Info("executing template", "title", data.Title, "url", data.URL, "hasCSS", data.HasCSS)
	err = ts.Execute(w, data)
	if err != nil {
		logger.Error("template", "err", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (web *webRouter) readHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := web.db
	logger := web.cfg.Logger
	cfg := web.cfg

	posts, err := dbpool.FindPopularPosts()
	if err != nil {
		logger.Error("finding posts", "err", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := router.RenderTemplate(staticDir, []string{
		cfg.StaticPath("html/read.page.tmpl"),
	})

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	data := ReadPageData{
		Site: cfg.SiteData(),
	}

	curl := shared.NewCreateURL(cfg.Protocol, cfg.Domain)
	for _, post := range posts {
		item := PostItemData{
			URL:            template.URL(curl.FullPostURL(post.Username, post.Slug)),
			BlogURL:        template.URL(curl.FullBlogURL(post.Username)),
			Title:          shared.FilenameToTitle(post.Filename, post.Title),
			Description:    post.Description,
			Username:       post.Username,
			PublishAt:      post.PublishAt.Format(time.DateOnly),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: shared.TimeAgo(post.UpdatedAt),
			UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
			Score:          post.Score,
		}
		data.Posts = append(data.Posts, item)
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error("template execute", "err", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (web *webRouter) rssBlogHandler(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	dbpool := web.db
	logger := web.cfg.Logger
	cfg := web.cfg

	user, err := dbpool.FindUserByName(username)
	if err != nil {
		logger.Info("rss feed not found", "user", username)
		http.Error(w, "rss feed not found", http.StatusNotFound)
		return
	}
	router.SetUser(r, user.Name)
	logger = shared.LoggerWithUser(logger, user)
	logger.Info("fetching blog rss")

	tag := r.URL.Query().Get("tag")
	pager := &db.Pager{Num: 10, Page: 0}
	var posts []*db.Post
	var p *db.Paginate[*db.Post]
	if tag == "" {
		p, err = dbpool.FindPostsByUser(pager, user.ID, cfg.Space)
	} else {
		p, err = dbpool.FindUserPostsByTag(pager, tag, user.ID, cfg.Space)
	}
	posts = p.Data

	if err != nil {
		logger.Error("find posts", "err", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := template.New("rss.page.tmpl").Funcs(router.FuncMap).ParseFiles(
		cfg.StaticPath("html/list.partial.tmpl"),
		cfg.StaticPath("html/rss.page.tmpl"),
	)
	if err != nil {
		logger.Error("template parse file", "err", err.Error())
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
			logger.Error("readme", "err", err.Error())
		}
		if parsedText.Title != "" {
			headerTxt.Title = parsedText.Title
		}

		if parsedText.Description != "" {
			headerTxt.Bio = parsedText.Description
		}
	}

	curl := shared.CreateURLFromRequest(cfg.Protocol, cfg.Domain, r)
	blogUrl := curl.FullBlogURL(username)

	updatedAt := &time.Time{}
	if len(posts) > 0 {
		updatedAt = posts[0].PublishAt
	}

	feed := &feeds.Feed{
		Id:          blogUrl,
		Title:       headerTxt.Title,
		Link:        &feeds.Link{Href: blogUrl},
		Description: headerTxt.Bio,
		Author:      &feeds.Author{Name: username},
		Created:     *user.CreatedAt,
		Updated:     *updatedAt,
	}

	var feedItems []*feeds.Item
	for _, post := range posts {
		if slices.Contains(cfg.HiddenPosts, post.Filename) {
			continue
		}

		content := ""
		ext := filepath.Ext(post.Filename)
		switch ext {
		case ".md":
			parsed, err := shared.ParseText(post.Text)
			if err != nil {
				logger.Error("parse post text", "err", err.Error())
			}

			footer, err := dbpool.FindPostWithFilename("_footer.md", user.ID, cfg.Space)
			var footerHTML string
			if err == nil {
				footerParsed, err := shared.ParseText(footer.Text)
				if err != nil {
					logger.Error("parse footer text", "err", err.Error())
				}
				footerHTML = footerParsed.Html
			}

			var tpl bytes.Buffer
			data := &PostPageData{
				Contents: template.HTML(parsed.Html + footerHTML),
			}
			if err := ts.Execute(&tpl, data); err != nil {
				logger.Error("md template", "err", err)
				continue
			}
			content = tpl.String()
		case ".lxt":
			parsed := shared.ListParseText(post.Text)
			var tpl bytes.Buffer
			data := &PostPageData{
				List: parsed,
			}
			if err := ts.Execute(&tpl, data); err != nil {
				logger.Error("lxt template", "err", err)
				continue
			}
			content = tpl.String()

		}

		realUrl := curl.FullPostURL(post.Username, post.Slug)
		feedId := realUrl

		item := &feeds.Item{
			Id:          feedId,
			Title:       shared.FilenameToTitle(post.Filename, post.Title),
			Link:        &feeds.Link{Href: realUrl},
			Content:     content,
			Updated:     *post.PublishAt,
			Created:     *post.PublishAt,
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
		logger.Error("feed to atom", "err", err.Error())
		http.Error(w, "Could not generate atom rss feed", http.StatusInternalServerError)
	}

	w.Header().Add("Content-Type", "application/atom+xml; charset=utf-8")
	_, err = w.Write([]byte(rss))
	if err != nil {
		logger.Error("writing to response handler", "err", err.Error())
	}
}

func (web *webRouter) rssHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := web.db
	logger := web.cfg.Logger
	cfg := web.cfg

	posts, err := dbpool.FindPopularPosts()
	if err != nil {
		logger.Error("find all posts", "err", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ts, err := template.New("rss.page.tmpl").Funcs(router.FuncMap).ParseFiles(
		cfg.StaticPath("html/list.partial.tmpl"),
		cfg.StaticPath("html/rss.page.tmpl"),
	)
	if err != nil {
		logger.Error("template parse file", "err", err.Error())
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

	curl := shared.CreateURLFromRequest(cfg.Protocol, cfg.Domain, r)

	var feedItems []*feeds.Item
	for _, post := range posts {
		content := ""
		ext := filepath.Ext(post.Filename)
		switch ext {
		case ".md":
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
			content = tpl.String()
		case ".lxt":
			parsed := shared.ListParseText(post.Text)
			var tpl bytes.Buffer
			data := &PostPageData{
				List: parsed,
			}
			if err := ts.Execute(&tpl, data); err != nil {
				continue
			}
			content = tpl.String()

		}

		realUrl := curl.FullPostURL(post.Username, post.Slug)
		if !curl.Subdomain && !curl.UsernameInRoute {
			realUrl = fmt.Sprintf("%s://%s%s", cfg.Protocol, r.Host, realUrl)
		}

		item := &feeds.Item{
			Id:          realUrl,
			Title:       post.Title,
			Link:        &feeds.Link{Href: realUrl},
			Content:     content,
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
		logger.Error("feed to atom", "err", err.Error())
		http.Error(w, "Could not generate atom rss feed", http.StatusInternalServerError)
	}

	w.Header().Add("Content-Type", "application/atom+xml; charset=utf-8")
	_, err = w.Write([]byte(rss))
	if err != nil {
		logger.Error("write to response writer", "err", err.Error())
	}
}

func (web *webRouter) imgRequest(w http.ResponseWriter, r *http.Request) {
	logger := web.cfg.Logger
	st := web.storage
	dbpool := web.db
	username := router.GetUsernameFromRequest(r)
	user, err := dbpool.FindUserByName(username)
	if err != nil {
		logger.Error("could not find user", "username", username)
		http.Error(w, "could find user", http.StatusNotFound)
		return
	}
	router.SetUser(r, user.Name)
	logger = shared.LoggerWithUser(logger, user)

	rawname := r.PathValue("file")
	imgOpts := r.PathValue("opts")
	// we place all prose images inside a "prose" folder
	fname := filepath.Join("/prose", rawname)

	opts, err := storage.UriToImgProcessOpts(imgOpts)
	if err != nil {
		errMsg := fmt.Sprintf("error processing img options: %s", err.Error())
		logger.Error("error processing img options", "err", errMsg)
		http.Error(w, errMsg, http.StatusUnprocessableEntity)
		return
	}

	bucket, err := st.GetBucket(shared.GetAssetBucketName(user.ID))
	if err != nil {
		logger.Error("bucket", "err", err)
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	fp := filepath.Join(bucket.Name, fname)
	imgproxy := storage.NewImgProxy(fp, opts)
	imgproxy.ServeHTTP(w, r)
}

type webRouter struct {
	cfg     *Config
	db      DB
	storage storage.StorageServe
}

func (web *webRouter) static(mux *http.ServeMux) {
	cfg := web.cfg
	files := cfg.Static()
	mux.HandleFunc("GET /read.css", files.File("read.css", "text/css"))
	mux.HandleFunc("GET /reset.css", files.File("reset.css", "text/css"))
	mux.HandleFunc("GET /syntax.css", files.File("syntax.css", "text/css"))
	mux.HandleFunc("GET /card.png", files.File("card.png", "image/png"))
	mux.HandleFunc("GET /favicon-16x16.png", files.File("favicon-16x16.png", "image/png"))
	mux.HandleFunc("GET /favicon.ico", files.File("favicon.ico", "image/x-icon"))
	if cfg.Debug {
		router.CreatePProfRoutesMux(mux)
	}
}

// mainMux serves the discover feed and site pages.
func (web *webRouter) mainMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", web.readHandler)
	mux.HandleFunc("GET /read", web.readHandler)
	mux.HandleFunc("GET /check", router.CheckHandler(web.cfg.Domain, web.cfg.Space, web.db))
	mux.HandleFunc("GET /rss", web.rssHandler)
	mux.HandleFunc("GET /rss.atom", web.rssHandler)
	mux.Handle("GET /_metrics", promhttp.Handler())
	mux.HandleFunc("GET /robots.txt", web.cfg.Static().File("robots.txt", "text/plain"))
	web.static(mux)
	return mux
}

// sitesMux serves a user's blog on their subdomain or custom domain.
func (web *webRouter) sitesMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", web.blogHandler)
	mux.HandleFunc("GET /_styles.css", web.blogStyleHandler)
	mux.HandleFunc("GET /robots.txt", web.robotsHandler)
	for _, feed := range []string{"/rss", "/rss.xml", "/atom.xml", "/feed.xml", "/atom", "/blog/index.xml"} {
		mux.HandleFunc("GET "+feed, web.rssBlogHandler)
	}
	web.static(mux)
	mux.HandleFunc("GET /{path...}", web.pathRoute)
	return mux
}

var (
	imgWithOpts = regexp.MustCompile(`^(.+\.(?:jpg|jpeg|png|gif|webp|svg|ico))/(.+)$`)
	imgPath     = regexp.MustCompile(`^.+\.(?:jpg|jpeg|png|gif|webp|svg|ico)$`)
)

// pathRoute serves the site paths ServeMux patterns can't tell apart: a
// post's markdown, an image with or without processing options, and a post.
// A "/raw/{slug...}" pattern would redirect "/raw" away from a post named raw.
func (web *webRouter) pathRoute(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if slug, ok := strings.CutPrefix(path, "raw/"); ok && slug != "" {
		r.SetPathValue("slug", slug)
		web.postRawHandler(w, r)
		return
	}
	for _, ext := range []string{".md", ".lxt"} {
		if slug, ok := strings.CutSuffix(path, ext); ok && slug != "" {
			r.SetPathValue("slug", slug)
			web.postRawHandler(w, r)
			return
		}
	}
	if m := imgWithOpts.FindStringSubmatch(path); m != nil {
		r.SetPathValue("file", m[1])
		r.SetPathValue("opts", m[2])
		web.imgRequest(w, r)
		return
	}
	if imgPath.MatchString(path) {
		r.SetPathValue("file", path)
		web.imgRequest(w, r)
		return
	}
	if slug, ok := strings.CutSuffix(path, ".html"); ok && slug != "" {
		path = slug
	}
	r.SetPathValue("slug", path)
	web.postHandler(w, r)
}

func StartApiServer() {
	cfg := NewConfig("prose-web")
	dbpool := NewDB(postgres.NewDB(cfg.DbURL, cfg.Logger))
	defer func() {
		_ = dbpool.Close()
	}()
	logger := cfg.Logger

	adapter := storage.GetStorageTypeFromEnv()
	st, err := storage.NewStorage(cfg.Logger, adapter)
	if err != nil {
		logger.Error("loading storage", "err", err)
		return
	}

	web := &webRouter{cfg: cfg, db: dbpool, storage: st}
	handler := router.NewHostRouter(cfg.Domain, cfg.Space, web.mainMux(), web.sitesMux())
	handler = router.NewUserMetrics("prose", prometheus.DefaultRegisterer, router.UserMetricsOpts{}).Handler(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Info(
		"Starting server on port",
		"port", cfg.Port,
		"domain", cfg.Domain,
	)

	logger.Error(http.ListenAndServe(portStr, handler).Error())
}
