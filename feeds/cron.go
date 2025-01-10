package feeds

import (
	"crypto/tls"
	"errors"
	"fmt"
	html "html/template"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
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
	userAgent := "linux:feeds:v2 (by /u/pico-sh)"
	r.Header.Set("User-Agent", userAgent)
	r.Header.Set("Accept", "*/*")
	return c.RoundTripper.RoundTrip(r)
}

var httpClient = http.Client{
	Transport: &UserAgentTransport{
		&http.Transport{
			TLSClientConfig: &tls.Config{},
		},
	},
}

type FeedItemTmpl struct {
	GUID        string
	Title       string
	Link        string
	PublishedAt *time.Time
	Content     html.HTML
	Description html.HTML
}

type Feed struct {
	Title       string
	Link        string
	Description string
	Items       []*FeedItemTmpl
	FeedItems   []*gofeed.Item
}

type DigestFeed struct {
	Feeds        []*Feed
	Options      DigestOptions
	KeepAliveURL string
	DaysLeft     string
	ShowBanner   bool
}

type DigestOptions struct {
	InlineContent bool
}

func itemToTemplate(item *gofeed.Item) *FeedItemTmpl {
	return &FeedItemTmpl{
		Title:       item.Title,
		Link:        item.Link,
		PublishedAt: item.PublishedParsed,
		Description: html.HTML(item.Description),
		Content:     html.HTML(item.Content),
	}
}

func digestOptionToTime(lastDigest time.Time, interval string) time.Time {
	day := 24 * time.Hour
	if interval == "10min" {
		return lastDigest.Add(10 * time.Minute)
	} else if interval == "1hour" {
		return lastDigest.Add(1 * time.Hour)
	} else if interval == "6hour" {
		return lastDigest.Add(6 * time.Hour)
	} else if interval == "12hour" {
		return lastDigest.Add(12 * time.Hour)
	} else if interval == "1day" || interval == "" {
		return lastDigest.Add(1 * day)
	} else if interval == "7day" {
		return lastDigest.Add(7 * day)
	} else if interval == "30day" {
		return lastDigest.Add(30 * day)
	} else {
		return lastDigest
	}
}

// see if this feed item should be emailed to user.
func isValidItem(item *gofeed.Item, feedItems []*db.FeedItem) bool {
	for _, feedItem := range feedItems {
		if item.GUID == feedItem.GUID {
			return false
		}
	}

	return true
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

func (f *Fetcher) Validate(post *db.Post, parsed *shared.ListParsedText) error {
	lastDigest := post.Data.LastDigest
	if lastDigest == nil {
		return nil
	}

	now := time.Now().UTC()

	expiresAt := post.ExpiresAt
	if expiresAt != nil {
		if post.ExpiresAt.Before(now) {
			return fmt.Errorf("(%s) post has expired, skipping", post.ExpiresAt.Format(time.RFC3339))
		}
	}

	digestAt := digestOptionToTime(*lastDigest, parsed.DigestInterval)
	if digestAt.After(now) {
		return fmt.Errorf("(%s) not time to digest, skipping", digestAt.Format(time.RFC3339))
	}
	return nil
}

func (f *Fetcher) RunPost(logger *slog.Logger, user *db.User, post *db.Post) error {
	logger = logger.With("filename", post.Filename)
	logger.Info("running feed post")

	parsed := shared.ListParseText(post.Text)

	logger.Info("last digest at", "lastDigest", post.Data.LastDigest.Format(time.RFC3339))
	err := f.Validate(post, parsed)
	if err != nil {
		logger.Info("validation failed", "err", err)
		return nil
	}

	urls := []string{}
	for _, item := range parsed.Items {
		u := ""
		if item.IsText || item.IsURL {
			u = item.Value
		} else if item.IsURL {
			u = string(item.Value)
		}

		if u == "" {
			continue
		}

		_, err := url.Parse(string(item.URL))
		if err != nil {
			logger.Info("invalid url", "url", string(item.URL))
			continue
		}

		urls = append(urls, u)
	}

	now := time.Now().UTC()
	if post.ExpiresAt == nil {
		expiresAt := time.Now().AddDate(0, 12, 0)
		post.ExpiresAt = &expiresAt
	}
	_, err = f.db.UpdatePost(post)
	if err != nil {
		return err
	}

	subject := fmt.Sprintf("%s feed digest", post.Title)

	msgBody, err := f.FetchAll(logger, urls, parsed.InlineContent, user.Name, post)
	if err != nil {
		errForUser := err

		// we don't want to increment in this case
		if errors.Is(errForUser, ErrNoRecentArticles) {
			return nil
		}

		post.Data.Attempts += 1
		logger.Error("could not fetch urls", "err", err, "attempts", post.Data.Attempts)

		errBody := fmt.Sprintf(`There was an error attempting to fetch your feeds (%d) times.  After (5) attempts we remove the file from our system.  Please check all the URLs and re-upload.
Also, we have centralized logs in our pico.sh TUI that will display realtime feed errors so you can debug.


%s


%s`, post.Data.Attempts, errForUser.Error(), post.Text)
		err = f.SendEmail(
			logger, user.Name,
			parsed.Email,
			subject,
			&MsgBody{Html: strings.ReplaceAll(errBody, "\n", "<br />"), Text: errBody},
		)
		if err != nil {
			return err
		}

		if post.Data.Attempts >= 5 {
			err = f.db.RemovePosts([]string{post.ID})
			if err != nil {
				return err
			}
		} else {
			_, err = f.db.UpdatePost(post)
			if err != nil {
				return err
			}
		}
		return errForUser
	} else {
		post.Data.Attempts = 0
		_, err := f.db.UpdatePost(post)
		if err != nil {
			return err
		}
	}

	if msgBody != nil {
		err = f.SendEmail(logger, user.Name, parsed.Email, subject, msgBody)
		if err != nil {
			return err
		}
	}

	post.Data.LastDigest = &now
	_, err = f.db.UpdatePost(post)
	if err != nil {
		return err
	}

	return nil
}

func (f *Fetcher) RunUser(user *db.User) error {
	logger := shared.LoggerWithUser(f.cfg.Logger, user)
	posts, err := f.db.FindPostsForUser(&db.Pager{Num: 100}, user.ID, "feeds")
	if err != nil {
		return err
	}

	if len(posts.Data) > 0 {
		logger.Info("found feed posts", "len", len(posts.Data))
	}

	for _, post := range posts.Data {
		err = f.RunPost(logger, user, post)
		if err != nil {
			logger.Error("run post failed", "err", err)
		}
	}

	return nil
}

func (f *Fetcher) ParseURL(fp *gofeed.Parser, url string) (*gofeed.Feed, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
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

	feed, err := fp.ParseString(string(body))
	if err != nil {
		return nil, err
	}

	return feed, nil
}

func (f *Fetcher) Fetch(logger *slog.Logger, fp *gofeed.Parser, url string, username string, feedItems []*db.FeedItem) (*Feed, error) {
	logger.Info("fetching feed", "url", url)

	feed, err := f.ParseURL(fp, url)
	if err != nil {
		return nil, err
	}

	feedTmpl := &Feed{
		Title:       feed.Title,
		Description: feed.Description,
		Link:        feed.Link,
	}

	items := []*FeedItemTmpl{}
	gofeedItems := []*gofeed.Item{}
	// we only want to return feed items published since the last digest time we fetched
	for _, item := range feed.Items {
		if item == nil {
			continue
		}

		if !isValidItem(item, feedItems) {
			continue
		}

		gofeedItems = append(gofeedItems, item)
		items = append(items, itemToTemplate(item))
	}

	if len(items) == 0 {
		return nil, fmt.Errorf(
			"%s %w, skipping",
			url,
			ErrNoRecentArticles,
		)
	}

	feedTmpl.FeedItems = gofeedItems
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

func (f *Fetcher) FetchAll(logger *slog.Logger, urls []string, inlineContent bool, username string, post *db.Post) (*MsgBody, error) {
	fp := gofeed.NewParser()
	daysLeft := ""
	showBanner := false
	if post.ExpiresAt != nil {
		diff := time.Until(*post.ExpiresAt)
		daysLeftInt := int(math.Ceil(diff.Hours() / 24))
		daysLeft = fmt.Sprintf("%d", daysLeftInt)
		if daysLeftInt <= 30 {
			showBanner = true
		}
	}
	feeds := &DigestFeed{
		KeepAliveURL: fmt.Sprintf("https://feeds.pico.sh/keep-alive/%s", post.ID),
		DaysLeft:     daysLeft,
		ShowBanner:   showBanner,
		Options:      DigestOptions{InlineContent: inlineContent},
	}
	feedItems, err := f.db.FindFeedItemsByPostID(post.ID)
	if err != nil {
		return nil, err
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("feed file does not contain any urls")
	}

	var allErrors error
	for _, url := range urls {
		feedTmpl, err := f.Fetch(logger, fp, url, username, feedItems)
		if err != nil {
			if errors.Is(err, ErrNoRecentArticles) {
				logger.Info("no recent articles", "err", err)
			} else {
				allErrors = errors.Join(allErrors, fmt.Errorf("%s: %w", url, err))
				logger.Error("fetch error", "err", err)
			}
			continue
		}
		feeds.Feeds = append(feeds.Feeds, feedTmpl)
	}

	if len(feeds.Feeds) == 0 {
		if allErrors != nil {
			return nil, allErrors
		}
		return nil, fmt.Errorf("%w, skipping email", ErrNoRecentArticles)
	}

	fdi := []*db.FeedItem{}
	for _, feed := range feeds.Feeds {
		for _, item := range feed.FeedItems {
			fdi = append(fdi, &db.FeedItem{
				PostID: post.ID,
				GUID:   item.GUID,
				Data: db.FeedItemData{
					Title:       item.Title,
					Description: item.Description,
					Content:     item.Content,
					Link:        item.Link,
					PublishedAt: item.PublishedParsed,
				},
			})
		}
	}
	err = f.db.InsertFeedItems(post.ID, fdi)
	if err != nil {
		return nil, err
	}

	text, err := f.PrintText(feeds)
	if err != nil {
		return nil, err
	}

	html, err := f.PrintHtml(feeds)
	if err != nil {
		return nil, err
	}

	if allErrors != nil {
		text = fmt.Sprintf("> %s\n\n%s", allErrors, text)
		html = fmt.Sprintf("<blockquote>%s</blockquote><br /><br/>%s", allErrors, html)
	}

	return &MsgBody{
		Text: text,
		Html: html,
	}, nil
}

func (f *Fetcher) SendEmail(logger *slog.Logger, username, email string, subject string, msg *MsgBody) error {
	if email == "" {
		return fmt.Errorf("(%s) does not have an email associated with their feed post", username)
	}

	from := mail.NewEmail("team pico", shared.DefaultEmail)
	to := mail.NewEmail(username, email)

	// f.logger.Infof("message body (%s)", plainTextContent)

	message := mail.NewSingleEmail(from, subject, to, msg.Text, msg.Html)
	client := sendgrid.NewSendClient(f.cfg.SendgridKey)

	logger.Info("sending email digest")
	response, err := client.Send(message)
	if err != nil {
		return err
	}

	// f.logger.Infof("(%s) email digest response: %v", username, response)

	if len(response.Headers["X-Message-Id"]) > 0 {
		logger.Info(
			"successfully sent email digest",
			"email", email,
			"x-message-id", response.Headers["X-Message-Id"][0],
		)
	} else {
		logger.Error(
			"could not find x-message-id, which means sending an email failed",
			"email", email,
		)
	}

	return nil
}

func (f *Fetcher) Run(logger *slog.Logger) error {
	users, err := f.db.FindUsers()
	if err != nil {
		return err
	}

	for _, user := range users {
		err := f.RunUser(user)
		if err != nil {
			logger.Error("run user failed", "err", err)
			continue
		}
	}

	return nil
}

func (f *Fetcher) Loop() {
	logger := f.cfg.Logger
	for {
		logger.Info("running digest emailer")

		err := f.Run(logger)
		if err != nil {
			logger.Error("run failed", "err", err)
		}

		logger.Info("digest emailer finished, waiting 10 mins")
		time.Sleep(10 * time.Minute)
	}
}
