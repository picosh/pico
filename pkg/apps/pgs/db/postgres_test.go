package pgsdb

import (
	"log/slog"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/picosh/pico/pkg/db/postgres/pgtest"
)

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

func TestFindProjectsOrder(t *testing.T) {
	conn := sqlx.MustConnect("postgres", pgtest.URL(t))
	t.Cleanup(func() { _ = conn.Close() })
	pgtest.Clean(t, conn)
	testDB := NewDBWithConn(conn, slog.New(slog.DiscardHandler))

	var userID string
	if err := conn.Get(&userID, `INSERT INTO app_users (name) VALUES ('projectsowner') RETURNING id`); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	// "older" was created first but updated last, so the two orderings differ.
	now := time.Now()
	for _, p := range []struct {
		name      string
		createdAt time.Time
		updatedAt time.Time
	}{
		{"older", now.Add(-2 * time.Hour), now},
		{"newer", now.Add(-1 * time.Hour), now.Add(-1 * time.Hour)},
	} {
		id, err := testDB.InsertProject(userID, p.name, p.name)
		if err != nil {
			t.Fatalf("insert project %s: %v", p.name, err)
		}
		_, err = conn.Exec(`UPDATE projects SET created_at = $1, updated_at = $2 WHERE id = $3`, p.createdAt, p.updatedAt, id)
		if err != nil {
			t.Fatalf("set project %s times: %v", p.name, err)
		}
	}

	for by, want := range map[string][]string{
		"created_at": {"newer", "older"},
		"updated_at": {"older", "newer"},
	} {
		projects, err := testDB.FindProjects(by)
		if err != nil {
			t.Fatalf("FindProjects(%s) failed: %v", by, err)
		}
		var got []string
		for _, p := range projects {
			got = append(got, p.Name)
		}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("FindProjects(%s) = %v, want %v", by, got, want)
		}
	}
}
