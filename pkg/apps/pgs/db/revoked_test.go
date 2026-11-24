package pgsdb

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/picosh/pico/pkg/db/postgres/pgtest"
)

func testIsCertRevoked(t *testing.T, d *PgsPsqlDB) {
	t.Helper()
	ids := map[string]string{}
	for _, name := range []string{"revokeralice", "revokerbob"} {
		var id string
		if err := d.Db.Get(&id, `INSERT INTO app_users (name) VALUES ($1) RETURNING id`, name); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	for kind, value := range map[string]string{"key": "SHA256:a", "id": "ci"} {
		if _, err := d.Db.Exec(`INSERT INTO cert_revocations (user_id, kind, value) VALUES ($1, $2, $3)`, ids["revokeralice"], kind, value); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []struct {
		user, fp, id string
		want         bool
	}{
		{"revokeralice", "SHA256:a", "other", true},
		{"revokeralice", "SHA256:b", "ci", true},
		{"revokeralice", "SHA256:b", "", false},
		{"revokeralice", "ci", "SHA256:a", false},
		{"revokerbob", "SHA256:a", "ci", false},
	} {
		if got, err := d.IsCertRevoked(ids[c.user], c.fp, c.id); err != nil || got != c.want {
			t.Errorf("IsCertRevoked(%+v) = %v, %v", c, got, err)
		}
	}
}

func TestIsCertRevokedPostgres(t *testing.T) {
	conn := sqlx.MustConnect("postgres", pgtest.URL(t))
	t.Cleanup(func() { _ = conn.Close() })
	pgtest.Clean(t, conn)
	testIsCertRevoked(t, NewDBWithConn(conn, slog.New(slog.DiscardHandler)))
}

func TestIsCertRevokedSqlite(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	t.Run("new", func(t *testing.T) {
		d, err := NewSqliteDB(filepath.Join(t.TempDir(), "pgs.sqlite3"), logger)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = d.Close() }()
		testIsCertRevoked(t, d)
	})

	t.Run("upgraded", func(t *testing.T) {
		dsn := filepath.Join(t.TempDir(), "pgs.sqlite3")
		d, err := NewSqliteDB(dsn, logger)
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range []string{`DROP TABLE cert_revocations`, `PRAGMA user_version = 1`} {
			if _, err := d.Db.Exec(stmt); err != nil {
				t.Fatal(err)
			}
		}
		_ = d.Close()

		if d, err = NewSqliteDB(dsn, logger); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = d.Close() }()
		testIsCertRevoked(t, d)
	})
}
