package prose

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"slices"

	"github.com/gorilla/feeds"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	sst "github.com/picosh/pobj/storage"
	sendUtils "github.com/picosh/send/utils"
	"github.com/picosh/utils"
)

type SSG struct {
	Logger    *slog.Logger
	DB        db.DB
	Cfg       *shared.ConfigSite
	Storage   storage.StorageServe
	TmplDir   string
	StaticDir string
}

var Space = "prose"

func getPostTitle(post *db.Post) string {
	if post.Description == "" {
		return post.Title
	}

	return fmt.Sprintf("%s: %s", post.Title, post.Description)
}

func getBlogName(username string) string {
	return fmt.Sprintf("%s's blog", username)
}

func getBlogDomain(username, domain string) string {
	return fmt.Sprintf("%s.%s", username, domain)
}

func (ssg *SSG) tmpl(fpath string) string {
	return filepath.Join(ssg.TmplDir, fpath)
}

func (ssg *SSG) blogPage(w io.Writer, user *db.User, blog *UserBlogData, tag string) error {
	files := []string{
		ssg.tmpl("blog.page.tmpl"),
		ssg.tmpl("blog-default.partial.tmpl"),
		ssg.tmpl("blog-aside.partial.tmpl"),
		ssg.tmpl("footer.partial.tmpl"),
		ssg.tmpl("marketing-footer.partial.tmpl"),
		ssg.tmpl("base.layout.tmpl"),
	}
	ts, err := template.ParseFiles(files...)
	if err != nil {
		return err
	}

	headerTxt := &HeaderTxt{
		Title:      getBlogName(user.Name),
		Bio:        "",
		Layout:     "default",
		ImageCard:  "summary",
		WithStyles: true,
		Domain:     getBlogDomain(user.Name, ssg.Cfg.Domain),
	}
	readmeTxt := &ReadmeTxt{}
	readme := blog.Readme
	if readme != nil {
		parsedText, err := shared.ParseText(readme.Text)
		if err != nil {
			return err
		}
		headerTxt.Bio = parsedText.Description
		headerTxt.Layout = parsedText.Layout
		headerTxt.Image = template.URL(parsedText.Image)
		headerTxt.ImageCard = parsedText.ImageCard
		headerTxt.WithStyles = parsedText.WithStyles
		headerTxt.Favicon = template.URL(parsedText.Favicon)
		if parsedText.Title != "" {
			headerTxt.Title = parsedText.Title
		}
		if parsedText.Domain != "" {
			headerTxt.Domain = parsedText.Domain
		}

		headerTxt.Nav = []shared.Link{}
		for _, nav := range parsedText.Nav {
			finURL := nav.URL
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

	hasCSS := blog.CSS != nil
	postCollection := []PostItemData{}
	for _, post := range blog.Posts {
		if tag != "" {
			parsed, err := shared.ParseText(post.Text)
			if err != nil {
				blog.Logger.Error("post parse text", "err", err)
				continue
			}
			if !slices.Contains(parsed.Tags, tag) {
				continue
			}
		}

		p := PostItemData{
			URL: template.URL(
				fmt.Sprintf("/%s", post.Slug),
			),
			BlogURL:        template.URL("/"),
			Title:          utils.FilenameToTitle(post.Filename, post.Title),
			PublishAt:      post.PublishAt.Format(time.DateOnly),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: utils.TimeAgo(post.UpdatedAt),
			UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
		}
		postCollection = append(postCollection, p)
	}

	rssIdx := "/rss.atom"
	data := BlogPageData{
		Site:       *ssg.Cfg.GetSiteData(),
		PageTitle:  headerTxt.Title,
		URL:        template.URL(fmt.Sprintf("%s://%s", ssg.Cfg.Protocol, headerTxt.Domain)),
		RSSURL:     template.URL(rssIdx),
		Readme:     readmeTxt,
		Header:     headerTxt,
		Username:   user.Name,
		Posts:      postCollection,
		HasCSS:     hasCSS,
		CssURL:     template.URL("/_styles.css"),
		HasFilter:  tag != "",
		WithStyles: headerTxt.WithStyles,
	}

	return ts.Execute(w, data)
}

func (ssg *SSG) rssBlogPage(w io.Writer, user *db.User, blog *UserBlogData) error {
	ts, err := template.ParseFiles(ssg.tmpl("rss.page.tmpl"))
	if err != nil {
		return err
	}

	headerTxt := &HeaderTxt{
		Title:  getBlogName(user.Name),
		Domain: getBlogDomain(user.Name, ssg.Cfg.Domain),
	}

	readme := blog.Readme
	if readme != nil {
		parsedText, err := shared.ParseText(readme.Text)
		if err != nil {
			return err
		}
		if parsedText.Title != "" {
			headerTxt.Title = parsedText.Title
		}

		if parsedText.Description != "" {
			headerTxt.Bio = parsedText.Description
		}

		if parsedText.Domain != "" {
			headerTxt.Domain = parsedText.Domain
		}
	}

	blogUrl := fmt.Sprintf("%s://%s", ssg.Cfg.Protocol, headerTxt.Domain)

	feed := &feeds.Feed{
		Id:          blogUrl,
		Title:       headerTxt.Title,
		Link:        &feeds.Link{Href: blogUrl},
		Description: headerTxt.Bio,
		Author:      &feeds.Author{Name: user.Name},
		Created:     *user.CreatedAt,
	}

	var feedItems []*feeds.Item
	for _, post := range blog.Posts {
		if slices.Contains(ssg.Cfg.HiddenPosts, post.Filename) {
			continue
		}
		parsed, err := shared.ParseText(post.Text)
		if err != nil {
			return err
		}

		footer := blog.Footer
		var footerHTML string
		if footer != nil {
			footerParsed, err := shared.ParseText(footer.Text)
			if err != nil {
				return err
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

		realUrl := fmt.Sprintf("%s://%s/%s", ssg.Cfg.Protocol, headerTxt.Domain, post.Slug)
		feedId := realUrl

		item := &feeds.Item{
			Id:          feedId,
			Title:       utils.FilenameToTitle(post.Filename, post.Title),
			Link:        &feeds.Link{Href: realUrl},
			Content:     tpl.String(),
			Updated:     *post.UpdatedAt,
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
		return err
	}

	_, err = w.Write([]byte(rss))
	return err
}

func (ssg *SSG) writePostPage(w io.Writer, user *db.User, post *db.Post, blog *UserBlogData) (*shared.ParsedText, error) {
	blogName := getBlogName(user.Name)
	favicon := ""
	ogImage := ""
	ogImageCard := ""
	withStyles := true
	domain := getBlogDomain(user.Name, ssg.Cfg.Domain)
	var data PostPageData

	footer := blog.Footer
	var footerHTML template.HTML
	if footer != nil {
		footerParsed, err := shared.ParseText(footer.Text)
		if err != nil {
			return nil, err
		}
		footerHTML = template.HTML(footerParsed.Html)
	}

	// we need the blog name from the readme unfortunately
	readme := blog.Readme
	if readme != nil {
		readmeParsed, err := shared.ParseText(readme.Text)
		if err != nil {
			return nil, err
		}
		if readmeParsed.MetaData.Title != "" {
			blogName = readmeParsed.MetaData.Title
		}
		if readmeParsed.MetaData.Domain != "" {
			domain = readmeParsed.MetaData.Domain
		}
		withStyles = readmeParsed.WithStyles
		ogImage = readmeParsed.Image
		ogImageCard = readmeParsed.ImageCard
		favicon = readmeParsed.Favicon
	}

	diff := ""
	parsedText, err := shared.ParseText(post.Text)
	if err != nil {
		return nil, err
	}

	if parsedText.Image != "" {
		ogImage = parsedText.Image
	}

	if parsedText.ImageCard != "" {
		ogImageCard = parsedText.ImageCard
	}

	unlisted := false
	if post.Hidden || post.PublishAt.After(time.Now()) {
		unlisted = true
	}

	data = PostPageData{
		Site:      *ssg.Cfg.GetSiteData(),
		PageTitle: getPostTitle(post),
		URL: template.URL(
			fmt.Sprintf("%s://%s/%s", ssg.Cfg.Protocol, domain, post.Slug),
		),
		BlogURL:      "/",
		Description:  post.Description,
		Title:        utils.FilenameToTitle(post.Filename, post.Title),
		Slug:         post.Slug,
		PublishAt:    post.PublishAt.Format(time.DateOnly),
		PublishAtISO: post.PublishAt.Format(time.RFC3339),
		Username:     user.Name,
		BlogName:     blogName,
		Contents:     template.HTML(parsedText.Html),
		HasCSS:       blog.CSS != nil,
		CssURL:       template.URL("/_styles.css"),
		Tags:         parsedText.Tags,
		Image:        template.URL(ogImage),
		ImageCard:    ogImageCard,
		Favicon:      template.URL(favicon),
		Footer:       footerHTML,
		Unlisted:     unlisted,
		Diff:         template.HTML(diff),
		WithStyles:   withStyles,
	}

	files := []string{
		ssg.tmpl("post.page.tmpl"),
		ssg.tmpl("footer.partial.tmpl"),
		ssg.tmpl("marketing-footer.partial.tmpl"),
		ssg.tmpl("base.layout.tmpl"),
	}
	ts, err := template.ParseFiles(files...)
	if err != nil {
		return nil, err
	}

	return parsedText, ts.Execute(w, data)
}

func (ssg *SSG) discoverPage(w io.Writer) error {
	pager, err := ssg.DB.FindAllPosts(&db.Pager{Num: 50, Page: 0}, Space)
	if err != nil {
		return err
	}

	data := ReadPageData{
		Site: *ssg.Cfg.GetSiteData(),
	}

	for _, post := range pager.Data {
		item := PostItemData{
			URL: template.URL(
				fmt.Sprintf(
					"%s://%s/%s",
					ssg.Cfg.Protocol,
					getBlogDomain(post.Username, ssg.Cfg.Domain),
					post.Slug,
				),
			),
			BlogURL:        template.URL(getBlogDomain(post.Username, ssg.Cfg.Domain)),
			Title:          utils.FilenameToTitle(post.Filename, post.Title),
			Description:    post.Description,
			Username:       post.Username,
			PublishAt:      post.PublishAt.Format(time.DateOnly),
			PublishAtISO:   post.PublishAt.Format(time.RFC3339),
			UpdatedTimeAgo: utils.TimeAgo(post.UpdatedAt),
			UpdatedAtISO:   post.UpdatedAt.Format(time.RFC3339),
		}
		data.Posts = append(data.Posts, item)
	}

	files := []string{
		ssg.tmpl("read.page.tmpl"),
		ssg.tmpl("footer.partial.tmpl"),
		ssg.tmpl("marketing-footer.partial.tmpl"),
		ssg.tmpl("base.layout.tmpl"),
	}
	ts, err := template.ParseFiles(files...)
	if err != nil {
		return err
	}

	return ts.Execute(w, data)
}

func (ssg *SSG) discoverRssPage(w io.Writer) error {
	pager, err := ssg.DB.FindAllPosts(&db.Pager{Num: 25, Page: 0}, Space)
	if err != nil {
		return err
	}

	files := []string{
		ssg.tmpl("rss.page.tmpl"),
	}
	ts, err := template.ParseFiles(files...)
	if err != nil {
		return err
	}

	feed := &feeds.Feed{
		Title: fmt.Sprintf("%s discovery feed", ssg.Cfg.Domain),
		Link: &feeds.Link{
			Href: fmt.Sprintf("%s://%s", ssg.Cfg.Protocol, ssg.Cfg.Domain),
		},
		Description: fmt.Sprintf("%s latest posts", ssg.Cfg.Domain),
		Author:      &feeds.Author{Name: ssg.Cfg.Domain},
		Created:     time.Now(),
	}

	var feedItems []*feeds.Item
	for _, post := range pager.Data {
		parsed, err := shared.ParseText(post.Text)
		if err != nil {
			return err
		}

		var tpl bytes.Buffer
		data := &PostPageData{
			Contents: template.HTML(parsed.Html),
		}
		if err := ts.Execute(&tpl, data); err != nil {
			continue
		}

		realUrl := fmt.Sprintf(
			"%s://%s/%s",
			ssg.Cfg.Protocol,
			getBlogDomain(post.Username, ssg.Cfg.Domain),
			post.Slug,
		)

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
		return err
	}

	_, err = w.Write([]byte(rss))
	return err
}

func (ssg *SSG) upload(logger *slog.Logger, bucket sst.Bucket, fpath string, rdr io.Reader) error {
	toSite := filepath.Join("prose", fpath)
	logger.Info("uploading object", "bucket", bucket.Name, "object", toSite)
	buf := &bytes.Buffer{}
	size, err := io.Copy(buf, rdr)
	if err != nil {
		return err
	}

	_, _, err = ssg.Storage.PutObject(bucket, toSite, buf, &sendUtils.FileEntry{
		Mtime: time.Now().Unix(),
		Size:  size,
	})
	return err
}

func (ssg *SSG) notFoundPage(w io.Writer, user *db.User, blog *UserBlogData) error {
	ogImage := ""
	ogImageCard := ""
	favicon := ""
	contents := template.HTML("Oops!  we can't seem to find this post.")
	title := "Post not found"
	desc := "Post not found"
	hasCSS := blog.CSS != nil

	footer := blog.Footer
	var footerHTML template.HTML
	if footer != nil {
		footerParsed, err := shared.ParseText(footer.Text)
		if err != nil {
			return err
		}
		footerHTML = template.HTML(footerParsed.Html)
	}

	// we need the blog name from the readme unfortunately
	readme := blog.Readme
	if readme != nil {
		readmeParsed, err := shared.ParseText(readme.Text)
		if err != nil {
			return err
		}
		ogImage = readmeParsed.Image
		ogImageCard = readmeParsed.ImageCard
		favicon = readmeParsed.Favicon
	}

	notFound := blog.NotFound
	if notFound != nil {
		notFoundParsed, err := shared.ParseText(notFound.Text)
		if err != nil {
			blog.Logger.Error("could not parse markdown", "err", err.Error())
			return err
		}
		if notFoundParsed.MetaData.Title != "" {
			title = notFoundParsed.MetaData.Title
		}
		if notFoundParsed.MetaData.Description != "" {
			desc = notFoundParsed.MetaData.Description
		}
		ogImage = notFoundParsed.Image
		ogImageCard = notFoundParsed.ImageCard
		favicon = notFoundParsed.Favicon
		contents = template.HTML(notFoundParsed.Html)
	}

	data := PostPageData{
		Site:         *ssg.Cfg.GetSiteData(),
		BlogURL:      "/",
		PageTitle:    title,
		Description:  desc,
		Title:        title,
		PublishAt:    time.Now().Format(time.DateOnly),
		PublishAtISO: time.Now().Format(time.RFC3339),
		Username:     user.Name,
		BlogName:     getBlogName(user.Name),
		HasCSS:       hasCSS,
		CssURL:       template.URL("/_styles.css"),
		Image:        template.URL(ogImage),
		ImageCard:    ogImageCard,
		Favicon:      template.URL(favicon),
		Footer:       footerHTML,
		Contents:     contents,
		Unlisted:     true,
	}
	files := []string{
		ssg.tmpl("post.page.tmpl"),
		ssg.tmpl("footer.partial.tmpl"),
		ssg.tmpl("marketing-footer.partial.tmpl"),
		ssg.tmpl("base.layout.tmpl"),
	}
	ts, err := template.ParseFiles(files...)
	if err != nil {
		return err
	}
	return ts.Execute(w, data)
}

func (ssg *SSG) images(user *db.User, blog *UserBlogData) error {
	imgBucket, err := ssg.Storage.GetBucket(shared.GetImgsBucketName(user.ID))
	if err != nil {
		blog.Logger.Info("user does not have an images dir, skipping")
		return nil
	}
	imgs, err := ssg.Storage.ListObjects(imgBucket, "/", false)
	if err != nil {
		return err
	}

	for _, inf := range imgs {
		rdr, _, err := ssg.Storage.GetObject(imgBucket, inf.Name())
		if err != nil {
			return err
		}
		err = ssg.upload(blog.Logger, blog.Bucket, inf.Name(), rdr)
		if err != nil {
			return err
		}
	}

	return nil
}

func (ssg *SSG) static(logger *slog.Logger, bucket sst.Bucket) error {
	files, err := os.ReadDir(ssg.StaticDir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		fpath := filepath.Join(ssg.StaticDir, file.Name())
		fp, err := os.Open(fpath)
		if err != nil {
			return err
		}
		err = ssg.upload(logger, bucket, file.Name(), fp)
		if err != nil {
			return err
		}
	}

	return nil
}

func (ssg *SSG) Prose() error {
	ssg.Logger.Info("generating discover page")
	rdr, wtr := io.Pipe()
	go func() {
		err := ssg.discoverPage(wtr)
		wtr.Close()
		if err != nil {
			ssg.Logger.Error("discover page", "err", err)
		}
	}()

	user, err := ssg.DB.FindUserForName("pico")
	if err != nil {
		return err
	}

	bucketName := shared.GetAssetBucketName(user.ID)
	bucket, err := ssg.Storage.UpsertBucket(bucketName)
	if err != nil {
		return err
	}

	redirectsFile := "/rss /rss.atom 200\n"
	ssg.Logger.Info("generating _redirects file", "text", redirectsFile)
	// create redirects file
	redirects := strings.NewReader(redirectsFile)
	err = ssg.upload(ssg.Logger, bucket, "_redirects", redirects)
	if err != nil {
		return err
	}

	err = ssg.upload(ssg.Logger, bucket, "index.html", rdr)
	if err != nil {
		return err
	}

	ssg.Logger.Info("generating discover rss page")
	rdr, wtr = io.Pipe()
	go func() {
		err = ssg.discoverRssPage(wtr)
		wtr.Close()
		if err != nil {
			ssg.Logger.Error("discover rss page", "err", err)
		}
	}()

	err = ssg.upload(ssg.Logger, bucket, "rss.atom", rdr)
	if err != nil {
		return err
	}

	ssg.Logger.Info("copying static folder for root", "dir", ssg.StaticDir)
	err = ssg.static(ssg.Logger, bucket)
	if err != nil {
		return err
	}

	users, err := ssg.DB.FindUsers()
	if err != nil {
		return err
	}

	for _, user := range users {
		if user.Name != "erock" {
			continue
		}

		bucket, err := ssg.Storage.UpsertBucket(shared.GetAssetBucketName(user.ID))
		if err != nil {
			return err
		}

		err = ssg.ProseBlog(user, bucket)
		if err != nil {
			log := shared.LoggerWithUser(ssg.Logger, user)
			log.Error("could not generate blog for user", "err", err)
		}
	}

	return nil
}

func (ssg *SSG) PostPage(user *db.User, blog *UserBlogData, post *db.Post) (pt *shared.ParsedText, err error) {
	// create post file
	rdr, wtr := io.Pipe()
	var parsed *shared.ParsedText
	go func() {
		parsed, err = ssg.writePostPage(wtr, user, post, blog)
		wtr.Close()
		if err != nil {
			blog.Logger.Error("post page", "err", err)
		}
	}()

	fname := post.Slug + ".html"
	err = ssg.upload(blog.Logger, blog.Bucket, fname, rdr)
	if err != nil {
		return parsed, err
	}
	return parsed, nil
}

func (ssg *SSG) NotFoundPage(logger *slog.Logger, user *db.User, blog *UserBlogData) error {
	// create 404 page
	logger.Info("generating 404 page")
	rdr, wtr := io.Pipe()
	go func() {
		err := ssg.notFoundPage(wtr, user, blog)
		wtr.Close()
		if err != nil {
			blog.Logger.Error("not found page", "err", err)
		}
	}()

	err := ssg.upload(blog.Logger, blog.Bucket, "404.html", rdr)
	if err != nil {
		return err
	}

	return nil
}

func (ssg *SSG) findPost(username string, bucket sst.Bucket, filename string, modTime time.Time) (*db.Post, error) {
	updatedAt := modTime
	fp := filepath.Join("prose/", filename)
	logger := ssg.Logger.With("filename", fp)
	rdr, info, err := ssg.Storage.GetObject(bucket, fp)
	if err != nil {
		logger.Error("get object", "err", err)
		return nil, err
	}
	txtb, err := io.ReadAll(rdr)
	if err != nil {
		logger.Error("reader to string", "err", err)
		return nil, err
	}
	txt := string(txtb)
	parsed, err := shared.ParseText(txt)
	if err != nil {
		logger.Error("parse text", "err", err)
		return nil, err
	}
	if parsed.PublishAt == nil || parsed.PublishAt.IsZero() {
		ca := info.Metadata.Get("Date")
		if ca != "" {
			dt, err := time.Parse(time.RFC1123, ca)
			if err != nil {
				return nil, err
			}
			parsed.PublishAt = &dt
		}
	}

	slug := utils.SanitizeFileExt(filename)

	return &db.Post{
		IsVirtual:   true,
		Slug:        slug,
		Filename:    filename,
		FileSize:    len(txt),
		Text:        txt,
		PublishAt:   parsed.PublishAt,
		UpdatedAt:   &updatedAt,
		Hidden:      parsed.Hidden,
		Description: parsed.Description,
		Title:       utils.FilenameToTitle(filename, parsed.Title),
		Username:    username,
	}, nil
}

func (ssg *SSG) findPostByName(userID, username string, bucket sst.Bucket, filename string, modTime time.Time) (*db.Post, error) {
	post, err := ssg.findPost(username, bucket, filename, modTime)
	if err == nil {
		return post, nil
	}
	return ssg.DB.FindPostWithFilename(filename, userID, Space)
}

func (ssg *SSG) findPosts(blog *UserBlogData) ([]*db.Post, bool, error) {
	posts := []*db.Post{}
	blog.Logger.Info("finding posts")
	objs, _ := ssg.Storage.ListObjects(blog.Bucket, "prose/", true)
	if len(objs) > 0 {
		blog.Logger.Info("found posts in bucket, using them")
	}
	for _, obj := range objs {
		if obj.IsDir() {
			continue
		}

		ext := filepath.Ext(obj.Name())
		if ext == ".md" {
			post, err := ssg.findPost(blog.User.Name, blog.Bucket, obj.Name(), obj.ModTime())
			if err != nil {
				blog.Logger.Error("find post", "err", err, "filename", obj.Name())
				continue
			}
			posts = append(posts, post)
		}
	}

	// we found markdown files in the pgs site so the assumption is
	// the pgs site is now the source of truth and we can ignore the posts table
	if len(posts) > 0 {
		return posts, true, nil
	}

	blog.Logger.Info("no posts found in bucket, using posts table")
	data, err := ssg.DB.FindPostsForUser(&db.Pager{Num: 1000, Page: 0}, blog.User.ID, Space)
	if err != nil {
		return nil, false, err
	}
	return data.Data, false, nil
}

type UserBlogData struct {
	Bucket   sst.Bucket
	User     *db.User
	Posts    []*db.Post
	Readme   *db.Post
	Footer   *db.Post
	CSS      *db.Post
	NotFound *db.Post
	Logger   *slog.Logger
}

func (ssg *SSG) ProseBlog(user *db.User, bucket sst.Bucket) error {
	// programmatically generate redirects file based on aliases
	// and other routes that were in prose that need to be available
	redirectsFile := "/rss /rss.atom 301\n"
	logger := shared.LoggerWithUser(ssg.Logger, user)
	logger.Info("generating blog for user")

	_, err := ssg.DB.FindProjectByName(user.ID, "prose")
	if err != nil {
		_, err := ssg.DB.InsertProject(user.ID, "prose", "prose")
		if err != nil {
			return err
		}
		return ssg.ProseBlog(user, bucket)
	}

	blog := &UserBlogData{
		User:   user,
		Bucket: bucket,
		Logger: logger,
	}

	posts, isVirtual, err := ssg.findPosts(blog)
	if err != nil {
		// no posts found, bail on generating an empty blog
		// TODO: gen the index anyway?
		return nil
	}

	blog.Posts = posts

	css, _ := ssg.findPostByName(user.ID, user.Name, bucket, "_styles.css", time.Time{})
	if css != nil && !css.IsVirtual {
		stylerdr := strings.NewReader(css.Text)
		err = ssg.upload(blog.Logger, bucket, "_styles.css", stylerdr)
		if err != nil {
			return err
		}
	}
	blog.CSS = css

	readme, _ := ssg.findPostByName(user.ID, user.Name, bucket, "_readme.md", time.Time{})
	if readme != nil && !readme.IsVirtual {
		rdr := strings.NewReader(readme.Text)
		err = ssg.upload(blog.Logger, bucket, "_readme.md", rdr)
		if err != nil {
			return err
		}
	}
	blog.Readme = readme

	footer, _ := ssg.findPostByName(user.ID, user.Name, bucket, "_footer.md", time.Time{})
	if readme != nil && !readme.IsVirtual {
		rdr := strings.NewReader(footer.Text)
		err = ssg.upload(blog.Logger, bucket, "_footer.md", rdr)
		if err != nil {
			return err
		}
	}
	blog.Footer = footer

	notFound, _ := ssg.findPostByName(user.ID, user.Name, bucket, "_404.md", time.Time{})
	if notFound != nil && !notFound.IsVirtual {
		rdr := strings.NewReader(notFound.Text)
		err = ssg.upload(blog.Logger, bucket, "_404.md", rdr)
		if err != nil {
			return err
		}
	}
	blog.NotFound = notFound

	tagMap := map[string]string{}
	for _, post := range posts {
		if post.Slug == "" {
			logger.Warn("post slug empty, skipping")
			continue
		}

		logger.Info("generating post", "slug", post.Slug)

		parsed, err := ssg.PostPage(user, blog, post)
		if err != nil {
			return err
		}
		// add aliases to redirects file
		for _, alias := range parsed.Aliases {
			redirectsFile += fmt.Sprintf("%s %s 301\n", alias, "/"+post.Slug)
		}
		for _, tag := range parsed.Tags {
			tagMap[tag] = tag
		}

		// create raw post file
		// only generate md file if we dont already have it in our pgs site
		if !post.IsVirtual {
			fpath := post.Slug + ".md"
			mdRdr := strings.NewReader(post.Text)
			err = ssg.upload(blog.Logger, bucket, fpath, mdRdr)
			if err != nil {
				return err
			}
		}
	}

	err = ssg.NotFoundPage(logger, user, blog)
	if err != nil {
		return err
	}

	tags := []string{""}
	for k := range tagMap {
		tags = append(tags, k)
	}

	// create index files
	for _, tag := range tags {
		logger.Info("generating blog index page", "tag", tag)
		rdr, wtr := io.Pipe()
		go func() {
			err = ssg.blogPage(wtr, user, blog, tag)
			wtr.Close()
			if err != nil {
				blog.Logger.Error("blog page", "err", err)
			}
		}()

		fpath := "index.html"
		if tag != "" {
			fpath = fmt.Sprintf("index-%s.html", tag)
		}
		err = ssg.upload(blog.Logger, bucket, fpath, rdr)
		if err != nil {
			return err
		}
	}

	logger.Info("generating blog rss page", "tag", "")
	rdr, wtr := io.Pipe()
	go func() {
		err = ssg.rssBlogPage(wtr, user, blog)
		wtr.Close()
		if err != nil {
			blog.Logger.Error("blog rss page", "err", err)
		}
	}()

	fpath := "rss.atom"
	err = ssg.upload(blog.Logger, bucket, fpath, rdr)
	if err != nil {
		return err
	}

	logger.Info("generating _redirects file", "text", redirectsFile)
	// create redirects file
	redirects := strings.NewReader(redirectsFile)
	err = ssg.upload(blog.Logger, bucket, "_redirects", redirects)
	if err != nil {
		return err
	}

	logger.Info("copying static folder", "dir", ssg.StaticDir)
	err = ssg.static(blog.Logger, bucket)
	if err != nil {
		return err
	}

	if !isVirtual {
		logger.Info("copying images")
		err = ssg.images(user, blog)
		if err != nil {
			return err
		}
	}

	logger.Info("success!")
	return nil
}
