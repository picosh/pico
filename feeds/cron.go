package feeds

import (
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/shared"
	"github.com/mmcdole/gofeed"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"golang.org/x/exp/maps"
)

var day = 24 * time.Hour
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

func (f *Fetcher) Compile(userID string) ([]string, error) {
	feedMap := map[string]bool{}
	posts, err := f.db.FindPostsForUser(&db.Pager{Num: 1000}, userID, "feeds")
	if err != nil {
		return []string{}, err
	}

	for _, post := range posts.Data {
		parsed := shared.ListParseText(post.Text, shared.NewNullLinkify())
		for _, item := range parsed.Items {
			feedMap[item.Value] = true
		}
	}

	feeds := maps.Keys(feedMap)
	return feeds, nil
}

func (f *Fetcher) Fetch(fp *gofeed.Parser, url string) (*Feed, error) {
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
	// we only want to return feed items published within the last day
	yday := time.Now().AddDate(0, 0, -1).Truncate(day)
	for _, item := range feed.Items {
		if item.PublishedParsed.Truncate(day).Before(yday) {
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

func (f *Fetcher) FetchAll(urls []string) (string, error) {
	fp := gofeed.NewParser()
	feeds := &DigestFeed{}

	for _, url := range urls {
		feedTmpl, err := f.Fetch(fp, url)
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

func (f *Fetcher) SendEmail(user *db.User, msg string) error {
	if user.Email == "" {
		return fmt.Errorf("(%s) does not have an email associated with they account", user.Name)
	}

	from := mail.NewEmail("team pico", "notify@feeds.sh")
	subject := "feeds.sh daily digest"
	to := mail.NewEmail(user.Name, user.Email)

	plainTextContent := msg
	htmlContent := msg

	message := mail.NewSingleEmail(from, subject, to, plainTextContent, htmlContent)
	client := sendgrid.NewSendClient(f.cfg.SendgridKey)

	response, err := client.Send(message)
	if err != nil {
		return nil
	}

	f.cfg.Logger.Infof(
		"(%s) successfully sent email digest (x-message-id: %s)",
		user.Email,
		response.Headers["X-Message-Id"][0],
	)

	return nil
}

func (f *Fetcher) RunUser(user *db.User) error {
	f.cfg.Logger.Infof("(%s) fetching feeds for daily digest", user.Name)

	urls, err := f.Compile(user.ID)
	if err != nil {
		return err
	}

	output, err := f.FetchAll(urls)
	if err != nil {
		return err
	}

	f.SendEmail(user, output)

	return nil
}

func (f *Fetcher) Run() error {
	users, err := f.db.FindUsers()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	nowHour := now.Hour()

	for _, user := range users {
		f.cfg.Logger.Info(user.DigestAt)
		dg := user.DigestAt.Hour()
		// only send digests if the digest_at for the user is *after* current time
		// AND we haven't already sent a digest today.
		beforeDigestTime := dg > nowHour
		alreadyDigested := false
		if user.LastDigestAt != nil {
			alreadyDigested = user.LastDigestAt.Truncate(day).Before(now.Truncate(day))
		}
		noEmail := user.Email == ""

		f.cfg.Logger.Infof(
			"(%s) no email: %t, before disgest time: %t, already digested today: %t",
			user.Name,
			noEmail,
			beforeDigestTime,
			alreadyDigested,
		)
		if noEmail || beforeDigestTime || alreadyDigested {
			f.cfg.Logger.Infof("(%s) daily digest doesn't meet criteria, skipping", user.Name)
			continue
		}

		err := f.RunUser(user)
		if err != nil {
			f.cfg.Logger.Error(err)
			continue
		}

		err = f.db.SetLastDigest(user.ID)
		if err != nil {
			f.cfg.Logger.Error(err)
		}
	}

	return nil
}

func (f *Fetcher) Loop() {
	for {
		f.cfg.Logger.Info("running daily digest emailer")

		err := f.Run()
		if err != nil {
			f.cfg.Logger.Error(err)
		}

		f.cfg.Logger.Info("daily digest emailer finished, waiting 1 hour")
		time.Sleep(1 * time.Hour)
	}
}
