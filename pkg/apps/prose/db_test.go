package prose

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
	testDB.ClearPopularPostsCache()
}

func mustInsertPost(t *testing.T, post *db.Post) *db.Post {
	t.Helper()
	return pgtest.InsertPost(t, testDB, post)
}

func TestFindPopularPosts(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("feedowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI feedowner", "comment", "")

	now := time.Now()
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "feedpost.md", Slug: "feedpost", Title: "Feed Post", Space: "prose", PublishAt: &now})

	// Before having feature flag, should not appear
	posts, err := testDB.FindPopularPosts()
	if err != nil {
		t.Fatalf("FindPopularPosts failed: %v", err)
	}
	if len(posts) != 0 {
		t.Errorf("expected 0 posts without feature flag, got %d", len(posts))
	}

	// Add prose feature flag
	_, err = testDB.InsertFeature(user.ID, "prose", time.Now().Add(time.Hour*24))
	if err != nil {
		t.Fatalf("InsertFeature failed: %v", err)
	}
	testDB.ClearPopularPostsCache()

	posts, err = testDB.FindPopularPosts()
	if err != nil {
		t.Fatalf("FindPopularPosts failed: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post in popular posts, got %d", len(posts))
	}
	if posts[0].Score != 0 {
		t.Errorf("expected score 0, got %d", posts[0].Score)
	}

	// Add visits
	_ = testDB.InsertVisit(&db.AnalyticsVisits{UserID: user.ID, PostID: post.ID, IpAddress: "192.168.1.1", Status: 200})
	_ = testDB.InsertVisit(&db.AnalyticsVisits{UserID: user.ID, PostID: post.ID, IpAddress: "192.168.1.2", Status: 200})
	_ = testDB.InsertVisit(&db.AnalyticsVisits{UserID: user.ID, PostID: post.ID, IpAddress: "192.168.1.1", Status: 200}) // duplicate IP

	// Before clearing cache, should return cached result (score 0)
	cachedPosts, err := testDB.FindPopularPosts()
	if err != nil {
		t.Fatalf("FindPopularPosts failed: %v", err)
	}
	if cachedPosts[0].Score != 0 {
		t.Errorf("expected cached score 0, got %d", cachedPosts[0].Score)
	}

	// After clearing cache, should re-query and return updated score 2
	testDB.ClearPopularPostsCache()
	posts, err = testDB.FindPopularPosts()
	if err != nil {
		t.Fatalf("FindPopularPosts failed: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}
	if posts[0].Score != 2 {
		t.Errorf("expected score 2 (unique visitors), got %d", posts[0].Score)
	}

	// Verify max 3 posts per user
	testDB.ClearPopularPostsCache()
	post2Time := now.Add(-1 * time.Hour)
	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "feedpost2.md", Slug: "feedpost2", Title: "Feed Post 2", Space: "prose", PublishAt: &post2Time})
	post3Time := now.Add(-2 * time.Hour)
	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "feedpost3.md", Slug: "feedpost3", Title: "Feed Post 3", Space: "prose", PublishAt: &post3Time})
	post4Time := now.Add(-3 * time.Hour)
	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "feedpost4.md", Slug: "feedpost4", Title: "Feed Post 4", Space: "prose", PublishAt: &post4Time})

	posts, err = testDB.FindPopularPosts()
	if err != nil {
		t.Fatalf("FindPopularPosts failed: %v", err)
	}
	if len(posts) != 3 {
		t.Errorf("expected max 3 posts for single user, got %d", len(posts))
	}

	// Post older than 30 days should be excluded
	testDB.ClearPopularPostsCache()
	oldTime := now.Add(-31 * 24 * time.Hour)
	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "oldpost.md", Slug: "oldpost", Title: "Old Post", Space: "prose", PublishAt: &oldTime})

	posts, err = testDB.FindPopularPosts()
	if err != nil {
		t.Fatalf("FindPopularPosts failed: %v", err)
	}
	if len(posts) != 3 {
		t.Errorf("expected still 3 posts (old post excluded), got %d", len(posts))
	}
}

// ============ Tags Tests ============

func TestFindUserPostsByTag(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("usertagowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI usertagowner", "comment", "")
	now := time.Now()
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "usertag.md", Slug: "usertag", Title: "User Tag", Space: "prose", PublishAt: &now})
	_ = testDB.ReplaceTagsByPost([]string{"mytag"}, post.ID)

	pager := &db.Pager{Num: 10, Page: 0}
	result, err := testDB.FindUserPostsByTag(pager, "mytag", user.ID, "prose")
	if err != nil {
		t.Fatalf("FindUserPostsByTag failed: %v", err)
	}
	if len(result.Data) != 1 {
		t.Errorf("expected 1 post with tag, got %d", len(result.Data))
	}
}

// ============ Aliases Tests ============
