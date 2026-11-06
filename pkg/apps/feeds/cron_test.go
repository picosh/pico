package feeds

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
)

type fetchTestDB struct {
	DB
	items    []*db.FeedItem
	inserted []*db.FeedItem
}

func (d *fetchTestDB) FindFeedItemsByPostID(string) ([]*db.FeedItem, error) {
	return d.items, nil
}

func (d *fetchTestDB) InsertFeedItems(_ string, items []*db.FeedItem) error {
	d.inserted = items
	return nil
}

func TestDigestPartialFailureAndSeenItems(t *testing.T) {
	t.Chdir("../../..")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "private response body")
			return
		}
		_, _ = io.WriteString(w, strings.Replace(testFeed, "<guid>new</guid>", "<guid>seen</guid>", 1)+" ")
	}))
	defer server.Close()
	client, baseURL := fixtureFeedClient(t, server)
	database := &fetchTestDB{items: []*db.FeedItem{{GUID: "seen"}}}
	f := &Fetcher{client: client, cfg: &Config{}, db: database}
	_, err := f.FetchAll(t.Context(), slog.New(slog.DiscardHandler), []string{baseURL + "/rss"}, true, true, &db.Post{ID: "post"})
	if !errors.Is(err, ErrNoRecentArticles) || len(database.inserted) != 0 {
		t.Fatalf("already served items: %v, inserted %d", err, len(database.inserted))
	}
	database.items = nil
	badURL := baseURL + "/bad?debug=<script>"
	msg, err := f.FetchAll(t.Context(), slog.New(slog.DiscardHandler), []string{badURL, baseURL + "/rss"}, true, true, &db.Post{ID: "post"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Text, badURL) || !strings.Contains(msg.Text, "403 Forbidden") || !strings.Contains(msg.Html, "&lt;script&gt;") {
		t.Fatalf("missing useful error details: %+v", msg)
	}
	if strings.Contains(msg.Html, "<script>") || strings.Contains(msg.Text, "private response body") {
		t.Fatalf("unescaped or leaked error: %+v", msg)
	}
	if len(database.inserted) != 1 || database.inserted[0].GUID != "seen" {
		t.Fatalf("inserted items: %+v", database.inserted)
	}
}

func TestDigestDeadlineStopsRemainingFeeds(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/rss" {
			_, _ = io.WriteString(w, testFeed)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	client, baseURL := fixtureFeedClient(t, server)
	database := &fetchTestDB{}
	f := &Fetcher{client: client, cfg: &Config{}, db: database}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := f.FetchAll(ctx, slog.New(slog.DiscardHandler), []string{baseURL + "/rss", baseURL + "/slow", baseURL + "/remaining"}, true, true, &db.Post{ID: "post"})
	if !errors.Is(err, context.DeadlineExceeded) || requests.Load() != 2 {
		t.Fatalf("deadline: %v, requests %d", err, requests.Load())
	}
	if len(database.inserted) != 0 {
		t.Fatal("canceled digest marked articles as delivered")
	}
}

func TestRunPostPreservesFetchErrorWhenEmailFails(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	user := &db.User{ID: "user", Name: "tester"}
	posts := stub.NewPostsDB(logger, user)
	database := &fetchTestDB{DB: posts}
	post, err := posts.InsertPost(&db.Post{
		UserID: user.ID, Space: "feeds", Filename: "rss.txt",
		Text: "=: email me@pico.test\n=> http://127.0.0.1/rss\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	f := NewFetcher(database, &Config{Logger: logger})
	f.host = ""
	err = f.RunPost(t.Context(), logger, user, true, post, true, time.Now())
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1/rss") || !strings.Contains(err.Error(), "blocked") || !strings.Contains(err.Error(), "sending error email") {
		t.Fatalf("missing original fetch error: %v", err)
	}
}
