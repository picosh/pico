package postgres

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
)

func inviteTestUser(t *testing.T, name string, plus bool) *db.User {
	t.Helper()
	user, err := testDB.RegisterUser(name, "ssh-ed25519 fixture-"+name, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if plus {
		if _, err := testDB.InsertFeature(user.ID, "plus", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	return user
}

func TestInviteUser_Duplicate(t *testing.T) {
	cleanupTestData(t)
	from := inviteTestUser(t, "inviter", true)
	other := inviteTestUser(t, "otherinviter", true)
	to := inviteTestUser(t, "invitee", false)
	if err := testDB.InviteUser(from.ID, to.ID); err != nil {
		t.Fatal(err)
	}
	for _, sender := range []*db.User{from, other} {
		if err := testDB.InviteUser(sender.ID, to.ID); err == nil || !strings.Contains(err.Error(), "already been invited") {
			t.Fatalf("expected duplicate invite error, got %v", err)
		}
	}
	var invites, features int
	if err := testDB.Db.Get(&invites, `SELECT count(*) FROM invites WHERE to_user_id = $1`, to.ID); err != nil {
		t.Fatal(err)
	}
	if err := testDB.Db.Get(&features, `SELECT count(*) FROM feature_flags WHERE user_id = $1`, to.ID); err != nil {
		t.Fatal(err)
	}
	if invites != 1 || features != len(inviteFeatures) {
		t.Fatalf("got %d invites and %d feature grants", invites, features)
	}
	inviter, err := testDB.FindWhoInvitedUser(to.ID)
	if err != nil || inviter != from.Name {
		t.Fatalf("original inviter changed: %q, %v", inviter, err)
	}
}

func TestInviteUser_Concurrent(t *testing.T) {
	cleanupTestData(t)
	from := inviteTestUser(t, "inviter", true)
	to := inviteTestUser(t, "invitee", false)
	const attempts = 8
	start := make(chan struct{})
	results := make(chan error, attempts)
	for range attempts {
		go func() {
			<-start
			results <- testDB.InviteUser(from.ID, to.ID)
		}()
	}
	close(start)
	var successes int
	for range attempts {
		err := <-results
		if err == nil {
			successes++
		} else if !strings.Contains(err.Error(), "already been invited") {
			t.Errorf("unexpected invite error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful submissions, want 1", successes)
	}
}

func TestInviteUser_Rollback(t *testing.T) {
	cleanupTestData(t)
	from := inviteTestUser(t, "inviter", true)
	to := inviteTestUser(t, "invitee", false)
	_, err := testDB.Db.Exec(`ALTER TABLE feature_flags ADD CONSTRAINT reject_invite_feature
		CHECK (user_id != '` + to.ID + `' OR name != 'pastes') NOT VALID`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := testDB.Db.Exec(`ALTER TABLE feature_flags DROP CONSTRAINT reject_invite_feature`); err != nil {
			t.Error(err)
		}
	})
	if err := testDB.InviteUser(from.ID, to.ID); err == nil {
		t.Fatal("expected feature grant failure")
	}
	var rows int
	if err := testDB.Db.Get(&rows, `SELECT
		(SELECT count(*) FROM invites WHERE to_user_id = $1) +
		(SELECT count(*) FROM feature_flags WHERE user_id = $1)`, to.ID); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("failed invite left %d rows", rows)
	}
}

func TestInviteRecipientMigration(t *testing.T) {
	cleanupTestData(t)
	conn, err := testDB.Db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, err = conn.ExecContext(t.Context(), `CREATE TEMP TABLE invites (
		id int, from_user_id text, to_user_id text, created_at timestamp
	);
	INSERT INTO invites VALUES
		(1, 'later', 'recipient', '2026-11-02'),
		(2, 'first', 'recipient', '2026-11-01'),
		(3, 'tie', 'recipient', '2026-11-01'),
		(4, 'first', 'another', '2026-11-01');`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.ExecContext(t.Context(), `DROP TABLE pg_temp.invites`) }()
	migration, err := os.ReadFile("../../../sql/migrations/20261110_unique_invite_recipient.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := conn.ExecContext(t.Context(), string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	var ids string
	if err := conn.QueryRowContext(t.Context(), `SELECT string_agg(id::text, ',' ORDER BY id) FROM invites`).Scan(&ids); err != nil {
		t.Fatal(err)
	}
	if ids != "2,4" {
		t.Fatalf("migration retained invite IDs %q, want 2,4", ids)
	}
	if _, err := conn.ExecContext(t.Context(), `INSERT INTO invites VALUES (5, 'duplicate', 'recipient', NOW())`); err == nil {
		t.Fatal("expected unique recipient constraint")
	}
}
