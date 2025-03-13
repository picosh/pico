package shared

import (
	"fmt"
	"time"

	"github.com/gorilla/feeds"
	"github.com/picosh/pico/pkg/db"
)

func UserFeed(me db.DB, userID, token string) (*feeds.Feed, error) {
	var err error
	if token == "" {
		token, err = me.UpsertToken(userID, "pico-rss")
		if err != nil {
			return nil, err
		}
	}

	href := fmt.Sprintf("https://auth.pico.sh/rss/%s", token)

	feed := &feeds.Feed{
		Title:       "pico+",
		Link:        &feeds.Link{Href: href},
		Description: "get notified of important membership updates",
		Author:      &feeds.Author{Name: "team pico"},
	}
	var feedItems []*feeds.Item

	now := time.Now()
	ff, err := me.FindFeatureForUser(userID, "plus")
	if err != nil {
		// still want to send an empty feed
	} else {
		createdAt := ff.CreatedAt
		createdAtStr := createdAt.Format("2006-01-02 15:04:05")
		id := fmt.Sprintf("pico-plus-activated-%d", createdAt.Unix())
		content := `Thanks for joining pico+! You now have access to all our premium services for exactly one year.  We will send you pico+ expiration notifications through this RSS feed.  Go to <a href="https://pico.sh/getting-started#next-steps">pico.sh/getting-started#next-steps</a> to start using our services.`
		plus := &feeds.Item{
			Id:          id,
			Title:       fmt.Sprintf("pico+ membership activated on %s", createdAtStr),
			Link:        &feeds.Link{Href: "https://pico.sh"},
			Content:     content,
			Created:     *createdAt,
			Updated:     *createdAt,
			Description: content,
			Author:      &feeds.Author{Name: "team pico"},
		}
		feedItems = append(feedItems, plus)

		oneMonthWarning := ff.ExpiresAt.AddDate(0, -1, 0)
		mo := genFeedItem(now, *ff.ExpiresAt, oneMonthWarning, "1-month")
		if mo != nil {
			feedItems = append(feedItems, mo)
		}

		oneWeekWarning := ff.ExpiresAt.AddDate(0, 0, -7)
		wk := genFeedItem(now, *ff.ExpiresAt, oneWeekWarning, "1-week")
		if wk != nil {
			feedItems = append(feedItems, wk)
		}

		oneDayWarning := ff.ExpiresAt.AddDate(0, 0, -2)
		day := genFeedItem(now, *ff.ExpiresAt, oneDayWarning, "1-day")
		if day != nil {
			feedItems = append(feedItems, day)
		}
	}

	feed.Items = feedItems
	return feed, nil
}

func genFeedItem(now time.Time, expiresAt time.Time, warning time.Time, txt string) *feeds.Item {
	if now.After(warning) {
		content := fmt.Sprintf(
			"Your pico+ membership is going to expire on %s",
			expiresAt.Format("2006-01-02 15:04:05"),
		)
		return &feeds.Item{
			Id:          fmt.Sprintf("%d", warning.Unix()),
			Title:       fmt.Sprintf("pico+ %s expiration notice", txt),
			Link:        &feeds.Link{Href: "https://pico.sh"},
			Content:     content,
			Created:     warning,
			Updated:     warning,
			Description: content,
			Author:      &feeds.Author{Name: "team pico"},
		}
	}

	return nil
}
