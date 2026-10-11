package pastes

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

func TestFindExpiredPosts(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("expiredpostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI expiredpostowner", "comment", "")

	expired := time.Now().Add(-24 * time.Hour)
	_ = mustInsertPost(t, &db.Post{
		UserID:    user.ID,
		Filename:  "expired.txt",
		Slug:      "expired",
		Title:     "Expired",
		Space:     "pastes",
		ExpiresAt: &expired,
	})

	posts, err := testDB.FindExpiredPosts("pastes")
	if err != nil {
		t.Fatalf("FindExpiredPosts failed: %v", err)
	}
	if len(posts) != 1 {
		t.Errorf("expected 1 expired post, got %d", len(posts))
	}
}
