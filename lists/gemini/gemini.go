package gemini

import (
	"bytes"
	"context"
	"fmt"
	html "html/template"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"text/template"
	"time"

	"git.sr.ht/~adnano/go-gemini"
	"git.sr.ht/~adnano/go-gemini/certificate"
	feeds "git.sr.ht/~aw/gorilla-feeds"
	"git.sr.ht/~erock/lists.sh/internal"
	"git.sr.ht/~erock/lists.sh/pkg"
	"git.sr.ht/~erock/wish/cms/db"
	"git.sr.ht/~erock/wish/cms/db/postgres"
	"golang.org/x/exp/slices"
)

func renderTemplate(templates []string) (*template.Template, error) {
	files := make([]string, len(templates))
	copy(files, templates)
	files = append(
		files,
		"./gmi/footer.partial.tmpl",
		"./gmi/marketing-footer.partial.tmpl",
		"./gmi/base.layout.tmpl",
	)

	ts, err := template.ParseFiles(files...)
	if err != nil {
		return nil, err
	}
	return ts, nil
}

func createPageHandler(fname string) gemini.HandlerFunc {
	return func(ctx context.Context, w gemini.ResponseWriter, r *gemini.Request) {
		logger := GetLogger(ctx)
		cfg := GetCfg(ctx)
		ts, err := renderTemplate([]string{fname})

		if err != nil {
			logger.Error(err)
			w.WriteHeader(gemini.StatusTemporaryFailure, "Internal Service Error")
			return
		}

		data := internal.PageData{
			Site: *cfg.GetSiteData(),
		}
		err = ts.Execute(w, data)
		if err != nil {
			logger.Error(err)
			w.WriteHeader(gemini.StatusTemporaryFailure, "Internal Service Error")
		}
	}
}

func blogHandler(ctx context.Context, w gemini.ResponseWriter, r *gemini.Request) {
	username := GetField(ctx, 0)
	dbpool := GetDB(ctx)
	logger := GetLogger(ctx)
	cfg := GetCfg(ctx)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("blog not found: %s", username)
		w.WriteHeader(gemini.StatusNotFound, "blog not found")
		return
	}
	posts, err := dbpool.FindUpdatedPostsForUser(user.ID, cfg.Space)
	if err != nil {
		logger.Error(err)
		w.WriteHeader(gemini.StatusTemporaryFailure, "could not fetch posts for blog")
		return
	}

	ts, err := renderTemplate([]string{
		"./gmi/blog.page.tmpl",
		"./gmi/list.partial.tmpl",
	})

	if err != nil {
		logger.Error(err)
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
		return
	}

	headerTxt := &internal.HeaderTxt{
		Title: internal.GetBlogName(username),
		Bio:   "",
	}
	readmeTxt := &internal.ReadmeTxt{}

	postCollection := make([]internal.PostItemData, 0, len(posts))
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
			p := internal.PostItemData{
				URL:            html.URL(cfg.PostURL(post.Username, post.Filename)),
				BlogURL:        html.URL(cfg.BlogURL(post.Username)),
				Title:          internal.FilenameToTitle(post.Filename, post.Title),
				PublishAt:      post.PublishAt.Format("02 Jan, 2006"),
				PublishAtISO:   post.PublishAt.Format(time.RFC3339),
				UpdatedTimeAgo: internal.TimeAgo(post.UpdatedAt),
				UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
			}
			postCollection = append(postCollection, p)
		}
	}

	data := internal.BlogPageData{
		Site:      *cfg.GetSiteData(),
		PageTitle: headerTxt.Title,
		URL:       html.URL(cfg.BlogURL(username)),
		RSSURL:    html.URL(cfg.RssBlogURL(username)),
		Readme:    readmeTxt,
		Header:    headerTxt,
		Username:  username,
		Posts:     postCollection,
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err)
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
	}
}

func readHandler(ctx context.Context, w gemini.ResponseWriter, r *gemini.Request) {
	dbpool := GetDB(ctx)
	logger := GetLogger(ctx)
	cfg := GetCfg(ctx)

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pager, err := dbpool.FindAllUpdatedPosts(&db.Pager{Num: 30, Page: page}, cfg.Space)
	if err != nil {
		logger.Error(err)
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
		return
	}

	ts, err := renderTemplate([]string{
		"./gmi/read.page.tmpl",
	})

	if err != nil {
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
		return
	}

	nextPage := ""
	if page < pager.Total-1 {
		nextPage = fmt.Sprintf("/read?page=%d", page+1)
	}

	prevPage := ""
	if page > 0 {
		prevPage = fmt.Sprintf("/read?page=%d", page-1)
	}

	data := internal.ReadPageData{
		Site:     *cfg.GetSiteData(),
		NextPage: nextPage,
		PrevPage: prevPage,
	}

	longest := 0
	for _, post := range pager.Data {
		size := len(internal.TimeAgo(post.UpdatedAt))
		if size > longest {
			longest = size
		}
	}

	for _, post := range pager.Data {
		item := internal.PostItemData{
			URL:            html.URL(cfg.PostURL(post.Username, post.Filename)),
			BlogURL:        html.URL(cfg.BlogURL(post.Username)),
			Title:          internal.FilenameToTitle(post.Filename, post.Title),
			Description:    post.Description,
			Username:       post.Username,
			PublishAt:      post.PublishAt.Format("02 Jan, 2006"),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: internal.TimeAgo(post.UpdatedAt),
			UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
		}

		item.Padding = strings.Repeat(" ", longest-len(item.UpdatedTimeAgo))
		data.Posts = append(data.Posts, item)
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err)
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
	}
}

func postHandler(ctx context.Context, w gemini.ResponseWriter, r *gemini.Request) {
	username := GetField(ctx, 0)
	filename, _ := url.PathUnescape(GetField(ctx, 1))

	dbpool := GetDB(ctx)
	logger := GetLogger(ctx)
	cfg := GetCfg(ctx)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("blog not found: %s", username)
		w.WriteHeader(gemini.StatusNotFound, "blog not found")
		return
	}

	header, _ := dbpool.FindPostWithFilename("_header", user.ID, cfg.Space)
	blogName := internal.GetBlogName(username)
	if header != nil {
		headerParsed := pkg.ParseText(header.Text)
		if headerParsed.MetaData.Title != "" {
			blogName = headerParsed.MetaData.Title
		}
	}

	post, err := dbpool.FindPostWithFilename(filename, user.ID, cfg.Space)
	if err != nil {
		logger.Infof("post not found %s/%s", username, filename)
		w.WriteHeader(gemini.StatusNotFound, "post not found")
		return
	}

	parsedText := pkg.ParseText(post.Text)

	// we need the blog name from the readme unfortunately
	readme, err := dbpool.FindPostWithFilename("_readme", user.ID, cfg.Space)
	if err == nil {
		readmeParsed := pkg.ParseText(readme.Text)
		if readmeParsed.MetaData.Title != "" {
			blogName = readmeParsed.MetaData.Title
		}
	}

	_, err = dbpool.AddViewCount(post.ID)
	if err != nil {
		logger.Error(err)
	}

	data := internal.PostPageData{
		Site:         *cfg.GetSiteData(),
		PageTitle:    internal.GetPostTitle(post),
		URL:          html.URL(cfg.PostURL(post.Username, post.Filename)),
		BlogURL:      html.URL(cfg.BlogURL(username)),
		Description:  post.Description,
		ListType:     parsedText.MetaData.ListType,
		Title:        internal.FilenameToTitle(post.Filename, post.Title),
		PublishAt:    post.PublishAt.Format("02 Jan, 2006"),
		PublishAtISO: post.PublishAt.Format(time.RFC3339),
		Username:     username,
		BlogName:     blogName,
		Items:        parsedText.Items,
	}

	ts, err := renderTemplate([]string{
		"./gmi/post.page.tmpl",
		"./gmi/list.partial.tmpl",
	})

	if err != nil {
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
		return
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err)
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
	}
}

func transparencyHandler(ctx context.Context, w gemini.ResponseWriter, r *gemini.Request) {
	dbpool := GetDB(ctx)
	logger := GetLogger(ctx)
	cfg := GetCfg(ctx)

	analytics, err := dbpool.FindSiteAnalytics(cfg.Space)
	if err != nil {
		logger.Error(err)
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
		return
	}

	ts, err := template.ParseFiles(
		"./gmi/transparency.page.tmpl",
		"./gmi/footer.partial.tmpl",
		"./gmi/marketing-footer.partial.tmpl",
		"./gmi/base.layout.tmpl",
	)

	if err != nil {
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
		return
	}

	data := internal.TransparencyPageData{
		Site:      *cfg.GetSiteData(),
		Analytics: analytics,
	}
	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err)
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
	}
}

func rssBlogHandler(ctx context.Context, w gemini.ResponseWriter, r *gemini.Request) {
	username := GetField(ctx, 0)
	dbpool := GetDB(ctx)
	logger := GetLogger(ctx)
	cfg := GetCfg(ctx)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Infof("rss feed not found: %s", username)
		w.WriteHeader(gemini.StatusNotFound, "rss feed not found")
		return
	}
	posts, err := dbpool.FindUpdatedPostsForUser(user.ID, cfg.Space)
	if err != nil {
		logger.Error(err)
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
		return
	}

	ts, err := template.ParseFiles("./gmi/rss.page.tmpl", "./gmi/list.partial.tmpl")
	if err != nil {
		logger.Error(err)
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
		return
	}

	headerTxt := &internal.HeaderTxt{
		Title: internal.GetBlogName(username),
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
		if slices.Contains(internal.HiddenPosts, post.Filename) {
			continue
		}
		parsed := pkg.ParseText(post.Text)
		var tpl bytes.Buffer
		data := &internal.PostPageData{
			ListType: parsed.MetaData.ListType,
			Items:    parsed.Items,
		}
		if err := ts.Execute(&tpl, data); err != nil {
			continue
		}

		item := &feeds.Item{
			Id:      cfg.PostURL(post.Username, post.Filename),
			Title:   internal.FilenameToTitle(post.Filename, post.Title),
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
		w.WriteHeader(gemini.StatusTemporaryFailure, "Could not generate atom rss feed")
		return
	}

	// w.Header().Add("Content-Type", "application/atom+xml")
	_, err = w.Write([]byte(rss))
	if err != nil {
		logger.Error(err)
	}
}

func rssHandler(ctx context.Context, w gemini.ResponseWriter, r *gemini.Request) {
	dbpool := GetDB(ctx)
	logger := GetLogger(ctx)
	cfg := GetCfg(ctx)

	pager, err := dbpool.FindAllPosts(&db.Pager{Num: 25, Page: 0}, cfg.Space)
	if err != nil {
		logger.Error(err)
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
		return
	}

	ts, err := template.ParseFiles("./gmi/rss.page.tmpl", "./gmi/list.partial.tmpl")
	if err != nil {
		logger.Error(err)
		w.WriteHeader(gemini.StatusTemporaryFailure, err.Error())
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
		data := &internal.PostPageData{
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
		w.WriteHeader(gemini.StatusTemporaryFailure, "Could not generate atom rss feed")
	}

	// w.Header().Add("Content-Type", "application/atom+xml")
	_, err = w.Write([]byte(rss))
	if err != nil {
		logger.Error(err)
	}
}

func StartServer() {
	cfg := internal.NewConfigSite()
	db := postgres.NewDB(&cfg.ConfigCms)
	logger := cfg.Logger

	certificates := &certificate.Store{}
	certificates.Register("localhost")
	certificates.Register(cfg.Domain)
	certificates.Register(fmt.Sprintf("*.%s", cfg.Domain))
	if err := certificates.Load("/var/lib/gemini/certs"); err != nil {
		logger.Fatal(err)
	}

	routes := []Route{
		NewRoute("/", createPageHandler("./gmi/marketing.page.tmpl")),
		NewRoute("/spec", createPageHandler("./gmi/spec.page.tmpl")),
		NewRoute("/help", createPageHandler("./gmi/help.page.tmpl")),
		NewRoute("/ops", createPageHandler("./gmi/ops.page.tmpl")),
		NewRoute("/privacy", createPageHandler("./gmi/privacy.page.tmpl")),
		NewRoute("/transparency", transparencyHandler),
		NewRoute("/read", readHandler),
		NewRoute("/rss", rssHandler),
		NewRoute("/([^/]+)", blogHandler),
		NewRoute("/([^/]+)/rss", rssBlogHandler),
		NewRoute("/([^/]+)/([^/]+)", postHandler),
	}
	handler := CreateServe(routes, cfg, db, logger)
	router := gemini.HandlerFunc(handler)

	server := &gemini.Server{
		Addr:           "0.0.0.0:1965",
		Handler:        gemini.LoggingMiddleware(router),
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   1 * time.Minute,
		GetCertificate: certificates.Get,
	}

	// Listen for interrupt signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	errch := make(chan error)
	go func() {
		logger.Info("Starting server")
		ctx := context.Background()
		errch <- server.ListenAndServe(ctx)
	}()

	select {
	case err := <-errch:
		logger.Fatal(err)
	case <-c:
		// Shutdown the server
		logger.Info("Shutting down...")
		db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := server.Shutdown(ctx)
		if err != nil {
			logger.Fatal(err)
		}
	}
}
