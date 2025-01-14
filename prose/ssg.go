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

func (ssg *SSG) blogPage(w io.Writer, user *db.User, tag string) error {
	pager := &db.Pager{Num: 250, Page: 0}
	var err error
	var posts []*db.Post
	var p *db.Paginate[*db.Post]
	if tag == "" {
		p, err = ssg.DB.FindPostsForUser(pager, user.ID, Space)
	} else {
		p, err = ssg.DB.FindUserPostsByTag(pager, tag, user.ID, Space)
	}
	posts = p.Data

	if err != nil {
		return err
	}

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

	readme, err := ssg.DB.FindPostWithFilename("_readme.md", user.ID, Space)
	if err == nil {
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

	hasCSS := false
	_, err = ssg.DB.FindPostWithFilename("_styles.css", user.ID, Space)
	if err == nil {
		hasCSS = true
	}

	postCollection := make([]PostItemData, 0, len(posts))
	for _, post := range posts {
		p := PostItemData{
			URL: template.URL(
				fmt.Sprintf("/%s.html", post.Slug),
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

func (ssg *SSG) rssBlogPage(w io.Writer, user *db.User, tag string) error {
	var err error
	pager := &db.Pager{Num: 10, Page: 0}
	var posts []*db.Post
	var p *db.Paginate[*db.Post]
	if tag == "" {
		p, err = ssg.DB.FindPostsForUser(pager, user.ID, Space)
	} else {
		p, err = ssg.DB.FindUserPostsByTag(pager, tag, user.ID, Space)
	}

	if err != nil {
		return err
	}

	posts = p.Data

	ts, err := template.ParseFiles(ssg.tmpl("rss.page.tmpl"))
	if err != nil {
		return err
	}

	headerTxt := &HeaderTxt{
		Title:  getBlogName(user.Name),
		Domain: getBlogDomain(user.Name, ssg.Cfg.Domain),
	}

	readme, err := ssg.DB.FindPostWithFilename("_readme.md", user.ID, Space)
	if err == nil {
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
	for _, post := range posts {
		if slices.Contains(ssg.Cfg.HiddenPosts, post.Filename) {
			continue
		}
		parsed, err := shared.ParseText(post.Text)
		if err != nil {
			return err
		}

		footer, err := ssg.DB.FindPostWithFilename("_footer.md", user.ID, Space)
		var footerHTML string
		if err == nil {
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
		return err
	}

	_, err = w.Write([]byte(rss))
	return err
}

func (ssg *SSG) postPage(w io.Writer, user *db.User, post *db.Post) ([]string, error) {
	blogName := getBlogName(user.Name)
	favicon := ""
	ogImage := ""
	ogImageCard := ""
	hasCSS := false
	withStyles := true
	domain := getBlogDomain(user.Name, ssg.Cfg.Domain)
	var data PostPageData
	aliases := []string{}

	css, err := ssg.DB.FindPostWithFilename("_styles.css", user.ID, Space)
	if err == nil {
		if len(css.Text) > 0 {
			hasCSS = true
		}
	}

	footer, err := ssg.DB.FindPostWithFilename("_footer.md", user.ID, Space)
	var footerHTML template.HTML
	if err == nil {
		footerParsed, err := shared.ParseText(footer.Text)
		if err != nil {
			return aliases, err
		}
		footerHTML = template.HTML(footerParsed.Html)
	}

	// we need the blog name from the readme unfortunately
	readme, err := ssg.DB.FindPostWithFilename("_readme.md", user.ID, Space)
	if err == nil {
		readmeParsed, err := shared.ParseText(readme.Text)
		if err != nil {
			return aliases, err
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
		return aliases, err
	}

	if parsedText.Image != "" {
		ogImage = parsedText.Image
	}

	if parsedText.ImageCard != "" {
		ogImageCard = parsedText.ImageCard
	}

	aliases = parsedText.Aliases

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
		HasCSS:       hasCSS,
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
		return aliases, err
	}

	return aliases, ts.Execute(w, data)
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

func (ssg *SSG) upload(bucket sst.Bucket, fpath string, rdr io.Reader) error {
	toSite := filepath.Join("prose-blog", fpath)
	ssg.Logger.Info("uploading object", "bucket", bucket.Name, "object", toSite)
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

func (ssg *SSG) notFoundPage(w io.Writer, user *db.User) error {
	ogImage := ""
	ogImageCard := ""
	favicon := ""
	contents := template.HTML("Oops!  we can't seem to find this post.")
	title := "Post not found"
	desc := "Post not found"
	hasCSS := false

	css, err := ssg.DB.FindPostWithFilename("_styles.css", user.ID, Space)
	if err == nil {
		if len(css.Text) > 0 {
			hasCSS = true
		}
	}

	footer, err := ssg.DB.FindPostWithFilename("_footer.md", user.ID, Space)
	var footerHTML template.HTML
	if err == nil {
		footerParsed, err := shared.ParseText(footer.Text)
		if err != nil {
			return err
		}
		footerHTML = template.HTML(footerParsed.Html)
	}

	// we need the blog name from the readme unfortunately
	readme, err := ssg.DB.FindPostWithFilename("_readme.md", user.ID, Space)
	if err == nil {
		readmeParsed, err := shared.ParseText(readme.Text)
		if err != nil {
			return err
		}
		ogImage = readmeParsed.Image
		ogImageCard = readmeParsed.ImageCard
		favicon = readmeParsed.Favicon
	}

	notFound, err := ssg.DB.FindPostWithFilename("_404.md", user.ID, Space)
	if err == nil {
		notFoundParsed, err := shared.ParseText(notFound.Text)
		if err != nil {
			ssg.Logger.Error("could not parse markdown", "err", err.Error())
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

func (ssg *SSG) images(user *db.User, bucket sst.Bucket) error {
	imgBucket, err := ssg.Storage.GetBucket(shared.GetImgsBucketName(user.ID))
	if err != nil {
		ssg.Logger.Info("user does not have an images dir, skipping")
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
		err = ssg.upload(bucket, inf.Name(), rdr)
		if err != nil {
			return err
		}
	}

	return nil
}

func (ssg *SSG) static(bucket sst.Bucket) error {
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
		err = ssg.upload(bucket, file.Name(), fp)
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
	err = ssg.upload(bucket, "_redirects", redirects)
	if err != nil {
		return err
	}

	err = ssg.upload(bucket, "index.html", rdr)
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

	err = ssg.upload(bucket, "rss.atom", rdr)
	if err != nil {
		return err
	}

	ssg.Logger.Info("copying static folder for root", "dir", ssg.StaticDir)
	err = ssg.static(bucket)
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

func (ssg *SSG) ProseBlog(user *db.User, bucket sst.Bucket) error {
	// programmatically generate redirects file based on aliases
	// and other routes that were in prose that need to be available
	redirectsFile := "/rss /rss.atom 301\n"
	logger := shared.LoggerWithUser(ssg.Logger, user)

	data, err := ssg.DB.FindPostsForUser(&db.Pager{Num: 1000, Page: 0}, user.ID, Space)
	if err != nil {
		return err
	}

	// don't generate a site with 0 posts
	if data.Total == 0 {
		return nil
	}

	for _, post := range data.Data {
		if post.Slug == "" {
			logger.Warn("post slug empty, skipping")
			continue
		}

		logger.Info("generating post", "slug", post.Slug)
		fpath := fmt.Sprintf("%s.html", post.Slug)

		// create post file
		rdr, wtr := io.Pipe()
		go func() {
			aliases, err := ssg.postPage(wtr, user, post)
			wtr.Close()
			if err != nil {
				ssg.Logger.Error("post page", "err", err)
			}
			// add aliases to redirects file
			for _, alias := range aliases {
				redirectsFile += fmt.Sprintf("%s %s 200\n", alias, "/"+fpath)
			}
		}()

		err = ssg.upload(bucket, fpath, rdr)
		if err != nil {
			return err
		}

		// create raw post file
		fpath = post.Slug + ".md"
		mdRdr := strings.NewReader(post.Text)
		err = ssg.upload(bucket, fpath, mdRdr)
		if err != nil {
			return err
		}
	}

	// create 404 page
	logger.Info("generating 404 page")
	rdr, wtr := io.Pipe()
	go func() {
		err = ssg.notFoundPage(wtr, user)
		wtr.Close()
		if err != nil {
			ssg.Logger.Error("not found page", "err", err)
		}
	}()

	err = ssg.upload(bucket, "404.html", rdr)
	if err != nil {
		return err
	}

	tags, err := ssg.DB.FindTagsForUser(user.ID, Space)
	tags = append(tags, "")

	// create index files
	for _, tag := range tags {
		logger.Info("generating blog index page", "tag", tag)
		rdr, wtr := io.Pipe()
		go func() {
			err = ssg.blogPage(wtr, user, tag)
			wtr.Close()
			if err != nil {
				ssg.Logger.Error("blog page", "err", err)
			}
		}()

		fpath := "index.html"
		if tag != "" {
			fpath = fmt.Sprintf("index-%s.html", tag)
		}
		err = ssg.upload(bucket, fpath, rdr)
		if err != nil {
			return err
		}
	}

	logger.Info("generating blog rss page", "tag", "")
	rdr, wtr = io.Pipe()
	go func() {
		err = ssg.rssBlogPage(wtr, user, "")
		wtr.Close()
		if err != nil {
			ssg.Logger.Error("blog rss page", "err", err)
		}
	}()

	fpath := "rss.atom"
	err = ssg.upload(bucket, fpath, rdr)
	if err != nil {
		return err
	}

	logger.Info("generating _redirects file", "text", redirectsFile)
	// create redirects file
	redirects := strings.NewReader(redirectsFile)
	err = ssg.upload(bucket, "_redirects", redirects)
	if err != nil {
		return err
	}

	post, _ := ssg.DB.FindPostWithFilename("_styles.css", user.ID, Space)
	if post != nil {
		stylerdr := strings.NewReader(post.Text)
		err = ssg.upload(bucket, "_styles.css", stylerdr)
		if err != nil {
			return err
		}
	}

	logger.Info("copying static folder", "dir", ssg.StaticDir)
	err = ssg.static(bucket)
	if err != nil {
		return err
	}

	logger.Info("copying images")
	err = ssg.images(user, bucket)
	if err != nil {
		return err
	}

	return nil
}
