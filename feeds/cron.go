package feeds

import (
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
)

var ErrNoRecentArticles = errors.New("no recent articles")

type FeedItem struct {
	Title       string
	Link        string
	Description string
}

type Feed struct {
	Title       string
	Link        string
	Description string
	Items       []*FeedItem
}

type DigestFeed struct {
	Feeds []*Feed
}

type Fetcher struct {
	cfg *shared.ConfigSite
	db  db.DB
}

func NewFetcher(dbpool db.DB, cfg *shared.ConfigSite) *Fetcher {
	return &Fetcher{
		db:  dbpool,
		cfg: cfg,
	}
}

func digestOptionToTime(date time.Time, interval string) time.Time {
	day := 24 * time.Hour
	if interval == "10min" {
		date.Add(10 * time.Minute)
	} else if interval == "1hour" {
		date.Add(1 * time.Hour)
	} else if interval == "12hour" {
		date.Add(12 * time.Hour)
	} else if interval == "1day" || interval == "" {
		date.Add(day)
	} else if interval == "7day" {
		date.Add(7 * day)
	} else if interval == "30day" {
		date.Add(30 * day)
	}
	return date
}

func (f *Fetcher) Validate(lastDigest *time.Time, parsed *shared.ListParsedText) error {
	if lastDigest == nil {
		return nil
	}

	digestAt := digestOptionToTime(*lastDigest, parsed.DigestInterval)
	now := time.Now().UTC()
	if digestAt.Before(now) {
		return fmt.Errorf("(%s) not time to digest, skipping", digestAt)
	}
	return nil
}

func (f *Fetcher) RunPost(user *db.User, post *db.Post) error {
	f.cfg.Logger.Infof("(%s) running feed post (%s)", user.Name, post.Filename)
	parsed := shared.ListParseText(post.Text, shared.NewNullLinkify())
	err := f.Validate(post.Data.LastDigest, parsed)
	if err != nil {
		f.cfg.Logger.Info(err.Error())
		return nil
	}

	urls := []string{}
	for _, item := range parsed.Items {
		url := ""
		if item.IsText {
			url = item.Value
		} else if item.IsURL {
			url = string(item.URL)
		}

		if url == "" {
			continue
		}

		urls = append(urls, url)
	}

	txt, err := f.FetchAll(urls, post.Data.LastDigest)
	if err != nil {
		return err
	}

	err = f.SendEmail(user.Name, parsed.Email, txt)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	post.Data.LastDigest = &now
	_, err = f.db.UpdatePost(post)
	return err
}

func (f *Fetcher) RunUser(user *db.User) error {
	f.cfg.Logger.Infof("(%s) grabbing all feed posts", user.Name)
	posts, err := f.db.FindPostsForUser(&db.Pager{Num: 1000}, user.ID, "feeds")
	if err != nil {
		return err
	}

	for _, post := range posts.Data {
		err = f.RunPost(user, post)
		if err != nil {
			f.cfg.Logger.Infof(err.Error())
		}
	}

	return nil
}

func (f *Fetcher) Fetch(fp *gofeed.Parser, url string, lastDigest *time.Time) (*Feed, error) {
	feed, err := fp.ParseURL(url)
	if err != nil {
		return nil, err
	}

	feedTmpl := &Feed{
		Title:       feed.Title,
		Description: feed.Description,
		Link:        feed.Link,
	}
	items := []*FeedItem{}
	// we only want to return feed items published since the last digest time we fetched
	for _, item := range feed.Items {
		if lastDigest == nil || item.PublishedParsed.Before(*lastDigest) {
			continue
		}

		items = append(items, &FeedItem{
			Title:       item.Title,
			Link:        item.Link,
			Description: item.Description,
		})
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("(%s) %w, skipping", feed.FeedLink, ErrNoRecentArticles)
	}

	feedTmpl.Items = items
	return feedTmpl, nil
}

func (f *Fetcher) Print(feedTmpl *DigestFeed) (string, error) {
	ts, err := template.ParseFiles(
		f.cfg.StaticPath("html/digest.page.tmpl"),
	)

	if err != nil {
		return "", err
	}

	w := new(strings.Builder)
	err = ts.Execute(w, feedTmpl)
	if err != nil {
		return "", err
	}

	return w.String(), nil
}

func (f *Fetcher) FetchAll(urls []string, lastDigest *time.Time) (string, error) {
	fp := gofeed.NewParser()
	feeds := &DigestFeed{}

	for _, url := range urls {
		feedTmpl, err := f.Fetch(fp, url, lastDigest)
		if err != nil {
			if errors.Is(err, ErrNoRecentArticles) {
				f.cfg.Logger.Info(err)
			} else {
				f.cfg.Logger.Error(err)
			}
			continue
		}
		feeds.Feeds = append(feeds.Feeds, feedTmpl)
	}

	str, err := f.Print(feeds)
	if err != nil {
		return "", nil
	}

	return str, nil
}

func (f *Fetcher) SendEmail(username, email, msg string) error {
	if email == "" {
		return fmt.Errorf("(%s) does not have an email associated with their account", username)
	}

	from := mail.NewEmail("team pico", f.cfg.Email)
	subject := "feeds.sh daily digest"
	to := mail.NewEmail(username, email)

	plainTextContent := msg
	htmlContent := msg

	message := mail.NewSingleEmail(from, subject, to, plainTextContent, htmlContent)
	client := sendgrid.NewSendClient(f.cfg.SendgridKey)

	f.cfg.Logger.Infof("(%s) sending email digest", username)
	response, err := client.Send(message)
	if err != nil {
		return err
	}

	f.cfg.Logger.Infof(
		"(%s) successfully sent email digest (x-message-id: %s)",
		email,
		response.Headers["X-Message-Id"][0],
	)

	return nil
}

func (f *Fetcher) Run() error {
	users, err := f.db.FindUsers()
	if err != nil {
		return err
	}

	for _, user := range users {
		err := f.RunUser(user)
		if err != nil {
			f.cfg.Logger.Error(err)
			continue
		}
	}

	return nil
}

func (f *Fetcher) Loop() {
	for {
		f.cfg.Logger.Info("running digest emailer")

		err := f.Run()
		if err != nil {
			f.cfg.Logger.Error(err)
		}

		f.cfg.Logger.Info("digest emailer finished, waiting 10 mins")
		time.Sleep(10 * time.Minute)
	}
}
