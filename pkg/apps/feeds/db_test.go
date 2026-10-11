package feeds

import (
	"log/slog"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/db/postgres/pgtest"
)

var testDB *PsqlDB

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

func cleanupTestData(t *testing.T) {
	t.Helper()
	if testDB == nil {
		testDB = NewDB(postgres.NewDB(pgtest.URL(t), slog.New(slog.DiscardHandler)))
	}
	pgtest.Clean(t, testDB.Db)
}

func mustInsertPost(t *testing.T, post *db.Post) *db.Post {
	t.Helper()
	return pgtest.InsertPost(t, testDB, post)
}

func TestFindUsersWithPost(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("feedspostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI feedspostowner", "comment", "")

	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "feed.txt", Slug: "feed", Title: "Feed", Space: "feeds"})

	users, err := testDB.FindUsersWithPost("feeds")
	if err != nil {
		t.Fatalf("FindUsersWithPost failed: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("expected 1 user, got %d", len(users))
	}
}

func TestInsertFeedItems(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("feeditemsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI feeditemsowner", "comment", "")
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "feed.txt", Slug: "feed", Title: "Feed", Space: "feeds"})

	items := []*db.FeedItem{
		{PostID: post.ID, GUID: "guid-1", Data: db.FeedItemData{Title: "Item 1", Link: "http://example.com/1"}},
		{PostID: post.ID, GUID: "guid-2", Data: db.FeedItemData{Title: "Item 2", Link: "http://example.com/2"}},
	}

	err := testDB.InsertFeedItems(post.ID, items)
	if err != nil {
		t.Fatalf("InsertFeedItems failed: %v", err)
	}
}

func TestFindFeedItemsByPostID(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("findfeeditemsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findfeeditemsowner", "comment", "")
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "findfeed.txt", Slug: "findfeed", Title: "Find Feed", Space: "feeds"})

	items := []*db.FeedItem{
		{PostID: post.ID, GUID: "find-guid-1", Data: db.FeedItemData{Title: "Find Item 1"}},
	}
	_ = testDB.InsertFeedItems(post.ID, items)

	found, err := testDB.FindFeedItemsByPostID(post.ID)
	if err != nil {
		t.Fatalf("FindFeedItemsByPostID failed: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("expected 1 feed item, got %d", len(found))
	}
}

// ============ Projects Tests ============

func TestFeedItemData_JSONBRoundtrip(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("feedjsonbowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI feedjsonbowner", "comment", "")
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "feedjsonb.txt", Slug: "feedjsonb", Title: "Feed JSONB", Space: "feeds"})

	now := time.Now().Truncate(time.Second)
	items := []*db.FeedItem{
		{
			PostID: post.ID,
			GUID:   "jsonb-guid",
			Data: db.FeedItemData{
				Title:       "JSONB Item",
				Description: "Description",
				Content:     "Content",
				Link:        "http://example.com",
				PublishedAt: &now,
			},
		},
	}
	_ = testDB.InsertFeedItems(post.ID, items)

	found, err := testDB.FindFeedItemsByPostID(post.ID)
	if err != nil {
		t.Fatalf("FindFeedItemsByPostID failed: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 item, got %d", len(found))
	}
	if found[0].Data.Title != "JSONB Item" {
		t.Errorf("expected title 'JSONB Item', got '%s'", found[0].Data.Title)
	}
}
