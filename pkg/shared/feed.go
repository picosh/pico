package shared

import (
	"fmt"
	"sort"
	"time"

	"github.com/gorilla/feeds"
	"github.com/picosh/pico/pkg/db"
)

func genUserFeedTmpl(title, msg string) string {
	return fmt.Sprintf(`
<html>
	<head>
		<title>%s</title>
		<style>
			code {
				background-color: #ddd;
				border-radius: 5px;
				padding: 1px 3px;
			}
		</style>
	</head>
	<body>
		%s
	</body>
</html>
`, title, msg)
}

func PicoPlusFeed(expiration time.Time) string {
	msg := fmt.Sprintf(`<h1>Thanks for joining <code>pico+</code>!</h1>
<p>
	You now have access to all our premium services until <strong>%s</strong>.
</p>
<p>
	We will send you <code>pico+</code> expiration notifications through this RSS feed.
	Go to <a href="https://pico.sh/getting-started#next-steps">pico.sh/getting-started#next-steps</a>
	to start using our services.
</p>
<p>
	If you have any questions, please do not hesitate to <a href="https://pico.sh/contact">contact us</a>.
</p>`, expiration.Format(time.DateOnly))
	return genUserFeedTmpl("pico+ activated", msg)
}

func PicoPlusExpirationFeed(expiration time.Time, txt string, plusLink string) string {
	title := fmt.Sprintf("pico+ %s expiration notification!", txt)
	msg := fmt.Sprintf(`<h1>%s</h1>
<p>
	Your <code>pico+</code> membership will expire on <strong>%s</strong>.
</p>
<p>
	If your pico+ membership expires then we will:

	<ul>
		<li>revoke access to <a href="https">https://tuns.sh</a></li>
		<li>reject new sites being created for <a href="https://pgs.sh">pgs.sh</a></li>
		<li>revoke access to our IRC bouncer</li>
	</ul>
</p>
<p>
	In order to continue using our premium services, you need to purchase another year:
	<a href="%s">purchase pico+</a>
</p>
<p>
	If you have any questions, please do not hesitate to <a href="https://pico.sh/contact">contact us</a>.
</p>`, title, expiration.Format(time.DateOnly), plusLink)
	return genUserFeedTmpl(title, msg)
}

func UserFeed(me db.DB, user *db.User, token string) (*feeds.Feed, error) {
	var err error
	if token == "" {
		token, err = me.UpsertToken(user.ID, "pico-rss")
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
	ff, err := me.FindFeatureForUser(user.ID, "plus")
	if err != nil {
		// still want to send an empty feed
	} else {
		createdAt := ff.CreatedAt
		createdAtStr := createdAt.Format(time.RFC3339)
		id := fmt.Sprintf("pico-plus-activated-%d", createdAt.Unix())
		content := PicoPlusFeed(*ff.ExpiresAt)
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
		mo := genFeedItem(user.Name, now, *ff.ExpiresAt, oneMonthWarning, "1-month")
		if mo != nil {
			feedItems = append(feedItems, mo)
		}

		oneWeekWarning := ff.ExpiresAt.AddDate(0, 0, -7)
		wk := genFeedItem(user.Name, now, *ff.ExpiresAt, oneWeekWarning, "1-week")
		if wk != nil {
			feedItems = append(feedItems, wk)
		}

		oneDayWarning := ff.ExpiresAt.AddDate(0, 0, -2)
		day := genFeedItem(user.Name, now, *ff.ExpiresAt, oneDayWarning, "1-day")
		if day != nil {
			feedItems = append(feedItems, day)
		}
	}

	tunsLogs, _ := me.FindTunsEventLogs(user.ID)
	for _, eventLog := range tunsLogs {
		content := fmt.Sprintf(`Created At: %s<br />
Event type: %s<br />
Connection type: %s<br />
Remote addr: %s<br />
Tunnel type: %s<br />
Tunnel ID: %s<br />
Server: %s`,
			eventLog.CreatedAt.Format(time.RFC3339), eventLog.EventType, eventLog.ConnectionType,
			eventLog.RemoteAddr, eventLog.TunnelType, eventLog.TunnelID, eventLog.ServerID,
		)
		logItem := &feeds.Item{
			Id: fmt.Sprintf("%d", eventLog.CreatedAt.Unix()),
			Title: fmt.Sprintf(
				"%s tuns event for %s",
				eventLog.EventType, eventLog.TunnelID,
			),
			Link:        &feeds.Link{Href: "https://pico.sh"},
			Content:     content,
			Created:     *eventLog.CreatedAt,
			Updated:     *eventLog.CreatedAt,
			Description: content,
			Author:      &feeds.Author{Name: "team pico"},
		}
		feedItems = append(feedItems, logItem)
	}

	sort.Slice(feedItems, func(i, j int) bool {
		return feedItems[i].Created.After(feedItems[j].Created)
	})

	feed.Items = feedItems
	return feed, nil
}

func genFeedItem(userName string, now time.Time, expiresAt time.Time, warning time.Time, txt string) *feeds.Item {
	if now.After(warning) {
		content := PicoPlusExpirationFeed(
			expiresAt,
			txt,
			"https://auth.pico.sh/checkout/"+userName,
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
