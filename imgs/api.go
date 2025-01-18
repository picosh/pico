package imgs

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/pgs"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/utils"
)

type PostPageData struct {
	ImgURL template.URL
}

type BlogPageData struct {
	Site      *shared.SitePageData
	PageTitle string
	URL       template.URL
	Username  string
	Posts     []template.URL
}

var Space = "imgs"

func ImgsListHandler(w http.ResponseWriter, r *http.Request) {
	username := shared.GetUsernameFromRequest(r)
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	cfg := shared.GetCfg(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Info("blog not found", "username", username)
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}

	var posts []*db.Post
	pager := &db.Pager{Num: 1000, Page: 0}
	p, err := dbpool.FindPostsForUser(pager, user.ID, Space)
	posts = p.Data

	if err != nil {
		logger.Error(err.Error())
		http.Error(w, "could not fetch posts for blog", http.StatusInternalServerError)
		return
	}

	ts, err := shared.RenderTemplate(cfg, []string{
		cfg.StaticPath("html/imgs.page.tmpl"),
	})

	if err != nil {
		logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	curl := shared.CreateURLFromRequest(cfg, r)
	postCollection := make([]template.URL, 0, len(posts))
	for _, post := range posts {
		url := cfg.ImgURL(curl, post.Username, post.Slug)
		postCollection = append(postCollection, template.URL(url))
	}

	data := BlogPageData{
		Site:      cfg.GetSiteData(),
		PageTitle: fmt.Sprintf("%s imgs", username),
		URL:       template.URL(cfg.FullBlogURL(curl, username)),
		Username:  username,
		Posts:     postCollection,
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func anyPerm(proj *db.Project) bool {
	return true
}

func ImgRequest(w http.ResponseWriter, r *http.Request) {
	subdomain := shared.GetSubdomain(r)
	cfg := shared.GetCfg(r)
	st := shared.GetStorage(r)
	dbpool := shared.GetDB(r)
	logger := shared.GetLogger(r)
	username := shared.GetUsernameFromRequest(r)

	user, err := dbpool.FindUserForName(username)
	if err != nil {
		logger.Info("user not found", "user", username)
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	var imgOpts string
	var slug string
	if !cfg.IsSubdomains() || subdomain == "" {
		slug, _ = url.PathUnescape(shared.GetField(r, 1))
		imgOpts, _ = url.PathUnescape(shared.GetField(r, 2))
	} else {
		slug, _ = url.PathUnescape(shared.GetField(r, 0))
		imgOpts, _ = url.PathUnescape(shared.GetField(r, 1))
	}

	opts, err := storage.UriToImgProcessOpts(imgOpts)
	if err != nil {
		errMsg := fmt.Sprintf("error processing img options: %s", err.Error())
		logger.Info(errMsg)
		http.Error(w, errMsg, http.StatusUnprocessableEntity)
		return
	}

	// set default quality for web optimization
	if opts.Quality == 0 {
		opts.Quality = 80
	}

	ext := filepath.Ext(slug)
	// set default format to be webp
	if opts.Ext == "" && ext == "" {
		opts.Ext = "webp"
	}

	// Files can contain periods.  `filepath.Ext` is greedy and will clip the last period in the slug
	// and call that a file extension so we want to be explicit about what
	// file extensions we clip here
	for _, fext := range cfg.AllowedExt {
		if ext == fext {
			// users might add the file extension when requesting an image
			// but we want to remove that
			slug = utils.SanitizeFileExt(slug)
			break
		}
	}

	post, err := FindImgPost(r, user, slug)
	if err != nil {
		errMsg := fmt.Sprintf("image not found %s/%s", user.Name, slug)
		logger.Info(errMsg)
		http.Error(w, errMsg, http.StatusNotFound)
		return
	}

	fname := post.Filename
	router := pgs.NewWebRouter(
		cfg,
		logger,
		dbpool,
		st,
	)
	router.ServeAsset(fname, opts, true, anyPerm, w, r)
}

func FindImgPost(r *http.Request, user *db.User, slug string) (*db.Post, error) {
	dbpool := shared.GetDB(r)
	return dbpool.FindPostWithSlug(slug, user.ID, Space)
}
