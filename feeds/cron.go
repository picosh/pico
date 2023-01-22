package feeds

import (
	"errors"
	"fmt"
	html "html/template"
	"io"
	"net/http"
	"strconv"
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

type UserAgentTransport struct {
	http.RoundTripper
}

func (c *UserAgentTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("User-Agent", "linux:feeds:v1")
	return c.RoundTripper.RoundTrip(r)
}

type FeedItem struct {
	Title       string
	Link        string
	Content     html.HTML
	Description html.HTML
}

type Feed struct {
	Title       string
	Link        string
	Description string
	Items       []*FeedItem
}

type DigestFeed struct {
	Feeds   []*Feed
	Options DigestOptions
}

type DigestOptions struct {
	InlineContent bool
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
		return date.Add(10 * time.Minute)
	} else if interval == "1hour" {
		return date.Add(1 * time.Hour)
	} else if interval == "6hour" {
		return date.Add(6 * time.Hour)
	} else if interval == "12hour" {
		return date.Add(12 * time.Hour)
	} else if interval == "1day" || interval == "" {
		return date.Add(1 * day)
	} else if interval == "7day" {
		return date.Add(7 * day)
	} else if interval == "30day" {
		return date.Add(30 * day)
	} else {
		return date
	}
}

func (f *Fetcher) Validate(lastDigest *time.Time, parsed *shared.ListParsedText) error {
	if lastDigest == nil {
		return nil
	}

	digestAt := digestOptionToTime(*lastDigest, parsed.DigestInterval)
	now := time.Now().UTC()
	if digestAt.After(now) {
		return fmt.Errorf("(%s) not time to digest, skipping", digestAt)
	}
	return nil
}

func (f *Fetcher) RunPost(user *db.User, post *db.Post) error {
	f.cfg.Logger.Infof("(%s) running feed post (%s)", user.Name, post.Filename)

	parsed := shared.ListParseText(post.Text, shared.NewNullLinkify())

	f.cfg.Logger.Infof("Last digest at (%s)", post.Data.LastDigest)
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

	InlineContent, err := strconv.ParseBool(parsed.InlineContent)
	if err != nil {
		// its empty or its improperly configured, just send the content
		InlineContent = true
	}
	msgBody, err := f.FetchAll(urls, InlineContent, post.Data.LastDigest)
	if err != nil {
		return err
	}

	subject := fmt.Sprintf("%s feed digest", post.Title)
	err = f.SendEmail(user.Name, parsed.Email, subject, msgBody)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	post.Data.LastDigest = &now
	_, err = f.db.UpdatePost(post)
	return err
}

func (f *Fetcher) RunUser(user *db.User) error {
	posts, err := f.db.FindPostsForUser(&db.Pager{Num: 1000}, user.ID, "feeds")
	if err != nil {
		return err
	}

	if len(posts.Data) > 0 {
		f.cfg.Logger.Infof("(%s) found (%d) feed posts", user.Name, len(posts.Data))
	}

	for _, post := range posts.Data {
		err = f.RunPost(user, post)
		if err != nil {
			f.cfg.Logger.Infof(err.Error())
		}
	}

	return nil
}

func (f *Fetcher) ParseURL(fp *gofeed.Parser, url string) (*gofeed.Feed, error) {
	client := &http.Client{
		Transport: &UserAgentTransport{http.DefaultTransport},
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 300 {
		return nil, fmt.Errorf("fetching feed resulted in an error: %s %s", resp.Status, body)
	}

	if err != nil {
		return nil, err
	}

	feed, err := fp.ParseString(string(body))

	if err != nil {
		return nil, err
	}

	return feed, nil
}

func (f *Fetcher) Fetch(fp *gofeed.Parser, url string, lastDigest *time.Time) (*Feed, error) {
	f.cfg.Logger.Infof("(%s) fetching feed", url)

	feed, err := f.ParseURL(fp, url)
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
		if lastDigest != nil && item.PublishedParsed.Before(*lastDigest) {
			continue
		}

		items = append(items, &FeedItem{
			Title:       item.Title,
			Link:        item.Link,
			Content:     html.HTML(item.Content),
			Description: html.HTML(item.Description),
		})
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("(%s) %w, skipping", feed.FeedLink, ErrNoRecentArticles)
	}

	feedTmpl.Items = items
	return feedTmpl, nil
}

func (f *Fetcher) PrintText(feedTmpl *DigestFeed) (string, error) {
	ts, err := template.ParseFiles(
		f.cfg.StaticPath("html/digest_text.page.tmpl"),
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

func (f *Fetcher) PrintHtml(feedTmpl *DigestFeed) (string, error) {
	ts, err := html.ParseFiles(
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

type MsgBody struct {
	Html string
	Text string
}

func (f *Fetcher) FetchAll(urls []string, inlineContent bool, lastDigest *time.Time) (*MsgBody, error) {
	fp := gofeed.NewParser()
	feeds := &DigestFeed{Options: DigestOptions{InlineContent: inlineContent}}

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

	if len(feeds.Feeds) == 0 {
		return nil, fmt.Errorf("%w, skipping", ErrNoRecentArticles)
	}

	text, err := f.PrintText(feeds)
	if err != nil {
		return nil, err
	}

	html, err := f.PrintHtml(feeds)
	if err != nil {
		return nil, err
	}

	return &MsgBody{
		Text: text,
		Html: html,
	}, nil
}

func (f *Fetcher) SendEmail(username, email string, subject string, msg *MsgBody) error {
	if email == "" {
		return fmt.Errorf("(%s) does not have an email associated with their feed post", username)
	}

	from := mail.NewEmail("team pico", f.cfg.Email)
	to := mail.NewEmail(username, email)

	// f.cfg.Logger.Infof("message body (%s)", plainTextContent)

	message := mail.NewSingleEmail(from, subject, to, msg.Text, msg.Html)
	client := sendgrid.NewSendClient(f.cfg.SendgridKey)

	f.cfg.Logger.Infof("(%s) sending email digest", username)
	response, err := client.Send(message)
	if err != nil {
		return err
	}

	// f.cfg.Logger.Infof("(%s) email digest response: %v", username, response)

	if len(response.Headers["X-Message-Id"]) > 0 {
		f.cfg.Logger.Infof(
			"(%s) successfully sent email digest (x-message-id: %s)",
			email,
			response.Headers["X-Message-Id"][0],
		)
	} else {
		f.cfg.Logger.Errorf(
			"(%s) could not find x-message-id, which means sending an email failed",
			email,
		)
	}

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
