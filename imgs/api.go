package imgs

import (
	"bytes"
	"fmt"
	"html/template"
	"image"
	gif "image/gif"
	jpeg "image/jpeg"
	png "image/png"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/db/postgres"
	"git.sr.ht/~erock/pico/imgs/storage"
	"git.sr.ht/~erock/pico/shared"
	"github.com/gorilla/feeds"
	"github.com/kolesa-team/go-webp/encoder"
	"github.com/kolesa-team/go-webp/webp"
	"github.com/nfnt/resize"
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
		url := fmt.Sprintf(
			"%s/300x",
			cfg.ImgURL(post.Username, post.Slug, onSubdomain, withUserName),
		)
		postCollection = append(postCollection, &PostItemData{
			ImgURL:       template.URL(url),
			URL:          template.URL(cfg.ImgPostURL(post.Username, post.Slug, onSubdomain, withUserName)),
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

type deviceType int

const (
	desktopDevice deviceType = iota
)

type ImgOptimizer struct {
	// Specify the compression factor for RGB channels between 0 and 100. The default is 75.
	// A small factor produces a smaller file with lower quality.
	// Best quality is achieved by using a value of 100.
	Quality    float32
	Optimized  bool
	Width      uint
	Height     uint
	DeviceType deviceType
	Output     []byte
	Dimes      string
}

func (h *ImgOptimizer) GetImage(contents []byte, mimeType string) (image.Image, error) {
	r := bytes.NewReader(contents)
	switch mimeType {
	case "image/png":
		return png.Decode(r)
	case "image/jpeg":
		return jpeg.Decode(r)
	case "image/jpg":
		return jpeg.Decode(r)
	case "image/gif":
		return gif.Decode(r)
	}

	return nil, fmt.Errorf("(%s) not supported optimization", mimeType)
}

func (h *ImgOptimizer) GetRatio() error {
	if h.Dimes == "" {
		return nil
	}

	// dimes = x250 -- width is auto scaled and height is 250
	if strings.HasPrefix(h.Dimes, "x") {
		height, err := strconv.ParseUint(h.Dimes[1:], 10, 64)
		if err != nil {
			return err
		}
		h.Height = uint(height)
		return nil
	}

	// dimes = 250x -- width is 250 and height is auto scaled
	if strings.HasSuffix(h.Dimes, "x") {
		width, err := strconv.ParseUint(h.Dimes[:len(h.Dimes)-1], 10, 64)
		if err != nil {
			return err
		}
		h.Width = uint(width)
		return nil
	}

	res := strings.Split(h.Dimes, "x")
	if len(res) != 2 {
		return fmt.Errorf("(%s) must be in format (x200, 200x, or 200x200)", h.Dimes)
	}

	width, err := strconv.ParseUint(res[0], 10, 64)
	if err != nil {
		return err
	}
	h.Width = uint(width)

	height, err := strconv.ParseUint(res[1], 10, 64)
	if err != nil {
		return err
	}
	h.Height = uint(height)

	return nil
}

type SubImager interface {
	SubImage(r image.Rectangle) image.Image
}

func (h *ImgOptimizer) Process(contents []byte, mimeType string) ([]byte, error) {
	if !h.Optimized {
		return contents, nil
	}

	img, err := h.GetImage(contents, mimeType)
	if err != nil {
		return []byte{}, err
	}

	nextImg := img
	if h.Height > 0 || h.Width > 0 {
		nextImg = resize.Resize(h.Width, h.Height, img, resize.Bicubic)
	}

	options, err := encoder.NewLossyEncoderOptions(
		encoder.PresetDefault,
		h.Quality,
	)
	if err != nil {
		return []byte{}, err
	}

	output := &bytes.Buffer{}
	err = webp.Encode(output, nextImg, options)
	if err != nil {
		return []byte{}, err
	}

	if mimeType == "image/png" {
		fmt.Println(output)
	}
	return output.Bytes(), nil
}

func NewImgOptimizer(logger *zap.SugaredLogger, optimized bool, dimes string) *ImgOptimizer {
	opt := &ImgOptimizer{
		Optimized:  optimized,
		DeviceType: desktopDevice,
		Quality:    75,
		Dimes:      dimes,
	}

	err := opt.GetRatio()
	if err != nil {
		logger.Error(err)
	}
	return opt
}

type ImgHandler struct {
	Username  string
	Subdomain string
	Slug      string
	Cfg       *shared.ConfigSite
	Dbpool    db.DB
	Storage   storage.ObjectStorage
	Logger    *zap.SugaredLogger
	Img       *ImgOptimizer
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
	contents, err := h.Storage.GetFile(bucket, post.Filename)
	if err != nil {
		h.Logger.Infof("file not found %s/%s", h.Username, post.Filename)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if h.Img.Optimized {
		w.Header().Add("Content-Type", "image/webp")
	} else {
		w.Header().Add("Content-Type", post.MimeType)
	}

	contentsProc, err := h.Img.Process(contents, strings.TrimSpace(post.MimeType))
	if err != nil {
		h.Logger.Error(err)
	}

	_, err = w.Write(contentsProc)
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
		Username:  username,
		Subdomain: subdomain,
		Slug:      slug,
		Cfg:       cfg,
		Dbpool:    dbpool,
		Storage:   st,
		Logger:    logger,
		Img:       NewImgOptimizer(logger, false, ""),
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
		Username:  username,
		Subdomain: subdomain,
		Slug:      slug,
		Cfg:       cfg,
		Dbpool:    dbpool,
		Storage:   st,
		Logger:    logger,
		Img:       NewImgOptimizer(logger, true, dimes),
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
	hostDomain := strings.Split(r.Host, ":")[0]
	appDomain := strings.Split(cfg.ConfigCms.Domain, ":")[0]

	onSubdomain := cfg.IsSubdomains() && strings.Contains(hostDomain, appDomain)
	withUserName := (!onSubdomain && hostDomain == appDomain) || !cfg.IsCustomdomains()

	var data PostPageData
	post, err := dbpool.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err == nil {
		linkify := NewImgsLinkify(username, onSubdomain, withUserName)
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
			ImgURL:       template.URL(cfg.ImgURL(username, post.Slug, onSubdomain, withUserName)),
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
			ImgURL: template.URL(cfg.ImgURL(username, post.Slug, onSubdomain, withUserName)),
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
			ImgURL: template.URL(cfg.ImgURL(post.Username, post.Slug, onSubdomain, withUserName)),
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
		shared.NewRoute("GET", "/([^/]+)/o/([^/]+)", imgRequestOriginal),
		shared.NewRoute("GET", "/([^/]+)/p/([^/]+)", postHandler),
		shared.NewRoute("GET", "/([^/]+)/([^/]+)", imgRequest),
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
