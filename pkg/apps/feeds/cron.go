package feeds

import (
	"context"
	"errors"
	"fmt"
	"html"
	htmltmpl "html/template"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/adhocore/gronx"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/mmcdole/gofeed"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
)

var ErrNoRecentArticles = errors.New("no recent articles")

type FeedItemTmpl struct {
	GUID        string
	Title       string
	Link        string
	PublishedAt *time.Time
	Content     htmltmpl.HTML
	Description htmltmpl.HTML
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
	UnsubURL     string
	DaysLeft     string
	ShowBanner   bool
	SizeWarning  bool
	IsPicoPlus   bool
}

type DigestOptions struct {
	InlineContent bool
}

func itemToTemplate(item *gofeed.Item) *FeedItemTmpl {
	return &FeedItemTmpl{
		Title:       item.Title,
		Link:        item.Link,
		PublishedAt: item.PublishedParsed,
		Description: htmltmpl.HTML(item.Description),
		Content:     htmltmpl.HTML(item.Content),
	}
}

func DigestIntervalToCron(interval string) string {
	switch interval {
	case "10min":
		return "*/10 * * * *"
	case "1hour":
		return "0 * * * *"
	case "6hour":
		return "0 */6 * * *"
	case "12hour":
		return "0 */12 * * *"
	case "1day", "":
		return "0 13 * * *"
	case "7day":
		return "0 13 * * 0"
	case "30day":
		return "0 13 1 * *"
	default:
		return "0 13 * * *"
	}
}

func getFeedItemID(logger *slog.Logger, item *gofeed.Item) string {
	guid := strings.ToValidUTF8(item.GUID, "")
	if item.GUID == "" {
		logger.Info("no <guid> found for feed item, using <link> instead for its unique id")
		return strings.ToValidUTF8(item.Link, "")
	}
	return guid
}

type Fetcher struct {
	cfg    *Config
	db     DB
	auth   sasl.Client
	gron   *gronx.Gronx
	host   string
	client *http.Client
}

func NewFetcher(dbpool DB, cfg *Config) *Fetcher {
	host := os.Getenv("PICO_SMTP_HOST")
	smtPass := os.Getenv("PICO_SMTP_PASS")
	emailLogin := os.Getenv("PICO_SMTP_USER")

	auth := sasl.NewPlainClient("", emailLogin, smtPass)
	gron := gronx.New()
	return &Fetcher{
		db:     dbpool,
		cfg:    cfg,
		auth:   auth,
		gron:   gron,
		host:   host,
		client: feedHTTPClient,
	}
}

func DateToMin(now time.Time) time.Time {
	return time.Date(
		now.Year(), now.Month(), now.Day(),
		now.Hour(), now.Minute(),
		0, 0, // zero out second and nano-second for cron
		now.Location(),
	)
}

func (f *Fetcher) Validate(logger *slog.Logger, post *db.Post, parsed *shared.ListParsedText, now time.Time) error {
	expiresAt := post.ExpiresAt
	if expiresAt != nil {
		if post.ExpiresAt.Before(now) {
			return fmt.Errorf("(%s) post has expired, skipping", post.ExpiresAt.Format(time.RFC3339))
		}
	}

	cron := parsed.Cron
	// support for posts with deprecated `digest_interval` property
	if parsed.DigestInterval != "" {
		cron = DigestIntervalToCron(parsed.DigestInterval)
	}
	logger.Info("found cron", "cron", cron)

	if !f.gron.IsValid(cron) {
		return fmt.Errorf("(%s) is invalid `cron`, skipping", cron)
	}

	dt := DateToMin(now)
	isDue, err := f.gron.IsDue(cron, dt)
	if err != nil {
		return fmt.Errorf("cron error, skipping; err: %w", err)
	}
	if !isDue {
		nextTime, _ := gronx.NextTickAfter(cron, dt, true)
		return fmt.Errorf(
			"cron not time to digest, skipping; cur run: %s, next run: %s",
			f.gron.C.GetRef(),
			nextTime,
		)
	}
	return nil
}

func (f *Fetcher) RunPost(ctx context.Context, logger *slog.Logger, user *db.User, isPicoPlus bool, post *db.Post, skipValidation bool, now time.Time) error {
	logger = logger.With("filename", post.Filename)
	logger.Info("running feed post")

	parsed := shared.ListParseText(post.Text)

	if parsed.Email == "" {
		logger.Error("post does not have an email associated, removing post")
		err := f.db.RemovePosts([]string{post.ID})
		if err != nil {
			return err
		}
	}

	if post.Data.LastDigest != nil {
		logger.Info("last digest", "at", post.Data.LastDigest.Format(time.RFC3339))
	} else {
		logger.Info("first time running post")
	}
	err := f.Validate(logger, post, parsed, now)
	if err != nil {
		logger.Info("validation failed", "err", err)
		if skipValidation {
			logger.Info("overriding validation error, continuing")
		} else {
			return nil
		}
	}
	logger.Info("validation success")

	urls := []string{}
	seenURLs := map[string]bool{}
	for _, item := range parsed.Items {
		rawUrl := ""
		if item.IsText {
			rawUrl = string(item.Value)
		} else if item.IsURL {
			rawUrl = string(item.URL)
		}

		if rawUrl == "" {
			continue
		}

		if seenURLs[rawUrl] {
			continue
		}
		seenURLs[rawUrl] = true

		logger.Info("found rss feed url", "url", rawUrl)
		urls = append(urls, rawUrl)
	}

	if post.ExpiresAt == nil {
		expiresAt := now.AddDate(0, 12, 0)
		post.ExpiresAt = &expiresAt
	}
	_, err = f.db.UpdatePost(post)
	if err != nil {
		return err
	}

	subject := fmt.Sprintf("%s feed digest", post.Filename)
	unsubURL := getUnsubURL(post)

	msgBody, err := f.FetchAll(ctx, logger, urls, parsed.InlineContent, isPicoPlus, post)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		errForUser := err

		// we don't want to increment in this case
		if errors.Is(errForUser, ErrNoRecentArticles) {
			return nil
		}

		post.Data.Attempts += 1
		logger.Error("could not fetch urls", "err", err, "attempts", post.Data.Attempts)

		maxAttempts := 100
		errBody := fmt.Sprintf(`There was an error attempting to fetch your feeds (%d) times.  After (%d) attempts we remove the file from our system.  Please check all the URLs and re-upload.
Also, we have centralized logs in our pico.sh TUI that will display realtime feed errors so you can debug.


%s


%s`, post.Data.Attempts, maxAttempts, errForUser.Error(), post.Text)
		err = f.SendEmail(
			logger,
			user.Name,
			parsed.Email,
			subject,
			unsubURL,
			&MsgBody{Html: strings.ReplaceAll(html.EscapeString(errBody), "\n", "<br />"), Text: errBody},
		)
		if err != nil {
			return errors.Join(errForUser, fmt.Errorf("sending error email: %w", err))
		}

		if post.Data.Attempts >= maxAttempts {
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
		err = f.SendEmail(logger, user.Name, parsed.Email, subject, unsubURL, msgBody)
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

func (f *Fetcher) RunUser(user *db.User, now time.Time) error {
	logger := shared.LoggerWithUser(f.cfg.Logger, user)
	logger.Info("run user")
	posts, err := f.db.FindPostsByUser(&db.Pager{Num: 100}, user.ID, "feeds")
	if err != nil {
		return err
	}
	isPicoPlus := db.HasPlusOrSpace(f.db, user.ID, "feeds")
	if !isPicoPlus {
		return fmt.Errorf("must have pico+ or feeds access to receive rss digests")
	}

	if len(posts.Data) > 0 {
		logger.Info("found feed posts", "len", len(posts.Data))
	}

	for _, post := range posts.Data {
		err = f.RunPost(context.Background(), logger, user, isPicoPlus, post, false, now)
		if err != nil {
			logger.Error("run post failed", "err", err)
		}
	}

	return nil
}

func (f *Fetcher) Fetch(ctx context.Context, logger *slog.Logger, fp *gofeed.Parser, url string, feedItems map[string]bool) (*Feed, error) {
	logger.Info("fetching feed", "url", url)

	feed, err := f.ParseURL(ctx, fp, url)
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

		if feedItems[getFeedItemID(logger, item)] {
			logger.Info("feed item already served", "guid", item.GUID)
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
	ts, err := htmltmpl.ParseFiles(
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

func getUnsubURL(post *db.Post) string {
	return fmt.Sprintf("https://feeds.pico.sh/unsub/%s", post.ID)
}

func (f *Fetcher) FetchAll(ctx context.Context, logger *slog.Logger, urls []string, inlineContent bool, isPicoPlus bool, post *db.Post) (*MsgBody, error) {
	fetchCtx, cancel := context.WithTimeoutCause(ctx, feedDigestTimeout, fmt.Errorf("digest exceeded the %s time limit: %w", feedDigestTimeout, context.DeadlineExceeded))
	defer cancel()
	logger.Info("fetching feeds", "inlineContent", inlineContent)
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
		UnsubURL:     getUnsubURL(post),
		DaysLeft:     daysLeft,
		ShowBanner:   showBanner,
		Options:      DigestOptions{InlineContent: inlineContent},
		IsPicoPlus:   isPicoPlus,
	}
	feedItems, err := f.db.FindFeedItemsByPostID(post.ID)
	if err != nil {
		return nil, err
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("feed file does not contain any urls")
	}
	seenItems := make(map[string]bool, len(feedItems))
	for _, item := range feedItems {
		seenItems[item.GUID] = true
	}

	var allErrors error
	processed := 0
	for _, url := range urls {
		if fetchCtx.Err() != nil {
			break
		}
		processed++
		feedTmpl, err := f.Fetch(fetchCtx, logger, fp, url, seenItems)
		if err != nil {
			if errors.Is(err, ErrNoRecentArticles) {
				logger.Info("no recent articles", "err", err)
			} else {
				allErrors = errors.Join(allErrors, fmt.Errorf("%s: %w", url, err))
				logger.Error("fetch error", "url", url, "err", err)
			}
			continue
		}
		feeds.Feeds = append(feeds.Feeds, feedTmpl)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if fetchCtx.Err() != nil {
		allErrors = errors.Join(allErrors, fmt.Errorf("digest stopped after %d of %d feeds: %w", processed, len(urls), context.Cause(fetchCtx)))
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
			uid := getFeedItemID(logger, item)
			fdi = append(fdi, &db.FeedItem{
				PostID: post.ID,
				GUID:   uid,
				Data: db.FeedItemData{
					Title:       strings.ToValidUTF8(item.Title, ""),
					Description: strings.ToValidUTF8(item.Description, ""),
					Content:     strings.ToValidUTF8(item.Content, ""),
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

	htmlBody, err := f.PrintHtml(feeds)
	if err != nil {
		return nil, err
	}

	// cap body size to prevent abuse
	if len(htmlBody)+len(text) > 5*shared.MB {
		feeds.Options.InlineContent = false
		feeds.SizeWarning = true
		htmlBody, err = f.PrintHtml(feeds)
		if err != nil {
			return nil, err
		}
	}

	if allErrors != nil {
		text = fmt.Sprintf("> %s\n\n%s", allErrors, text)
		htmlBody = fmt.Sprintf("<blockquote>%s</blockquote><br /><br/>%s", html.EscapeString(allErrors.Error()), htmlBody)
	}

	return &MsgBody{
		Text: text,
		Html: htmlBody,
	}, nil
}

func (f *Fetcher) SendEmail(logger *slog.Logger, username, email, subject, unsubURL string, msg *MsgBody) error {
	if email == "" {
		return fmt.Errorf("(%s) does not have an email associated with their feed post", username)
	}
	smtpAddr := f.host
	fromEmail := "hello@pico.sh"
	to := []string{email}
	headers := map[string]string{
		"From":             fromEmail,
		"Subject":          subject,
		"To":               email,
		"MIME-Version":     "1.0",
		"Content-Type":     `multipart/alternative; boundary="boundary123"`,
		"List-Unsubscribe": "<" + unsubURL + ">",
	}
	var content strings.Builder
	for k, v := range headers {
		fmt.Fprintf(&content, "%s: %s\r\n", k, v)
	}
	content.WriteString("\r\n")
	content.WriteString("\r\n--boundary123\r\n")
	content.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	content.WriteString("\r\n" + msg.Text + "\r\n")
	content.WriteString("--boundary123\r\n")
	content.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n")
	content.WriteString("\r\n" + msg.Html + "\r\n")
	content.WriteString("--boundary123--")

	reader := strings.NewReader(content.String())
	logger.Info("sending email digest")
	err := smtp.SendMail(
		smtpAddr,
		f.auth,
		fromEmail,
		to,
		reader,
	)
	return err
}

func (f *Fetcher) Run(now time.Time) error {
	users, err := f.db.FindUsersWithPost("feeds")
	if err != nil {
		return err
	}

	for _, user := range users {
		err := f.RunUser(user, now)
		if err != nil {
			f.cfg.Logger.Error("run user failed", "err", err)
			continue
		}
	}

	return nil
}

func (f *Fetcher) Loop() {
	logger := f.cfg.Logger
	for {
		logger.Info("running digest emailer")

		err := f.Run(time.Now().UTC())
		if err != nil {
			logger.Error("run failed", "err", err)
		}

		logger.Info("digest emailer finished, waiting 1min ...")
		time.Sleep(1 * time.Minute)
	}
}
