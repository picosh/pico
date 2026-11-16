package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
)

func TestAddPicoPlusUser_EmailIsData(t *testing.T) {
	cleanupTestData(t)
	user := inviteTestUser(t, "buyer", false)
	email := `o'brien"x@pico.test`
	if err := testDB.AddPicoPlusUser(user.Name, email, "lemonsqueezy", "1"); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := testDB.Db.Get(&stored, `SELECT data->>'email' FROM feature_flags WHERE user_id = $1 AND name = 'plus'`, user.ID); err != nil {
		t.Fatal(err)
	}
	if stored != email {
		t.Fatalf("email = %q, want %q", stored, email)
	}
}

func TestAddPicoPlusUser_DuplicateTx(t *testing.T) {
	cleanupTestData(t)
	user := inviteTestUser(t, "buyer", false)
	if err := testDB.AddPicoPlusUser(user.Name, "a@pico.test", "lemonsqueezy", "42"); err != nil {
		t.Fatal(err)
	}
	if err := testDB.AddPicoPlusUser(user.Name, "a@pico.test", "lemonsqueezy", "42"); !errors.Is(err, db.ErrPaymentRecorded) {
		t.Fatalf("second delivery err = %v, want ErrPaymentRecorded", err)
	}
	var flags int
	if err := testDB.Db.Get(&flags, `SELECT count(*) FROM feature_flags WHERE user_id = $1 AND name = 'plus'`, user.ID); err != nil {
		t.Fatal(err)
	}
	if flags != 1 {
		t.Fatalf("got %d plus flags, want 1", flags)
	}
}

func TestAddPicoPlusUser_ExtendsValidPlus(t *testing.T) {
	cleanupTestData(t)
	user := inviteTestUser(t, "renewer", false)
	if err := testDB.AddPicoPlusUser(user.Name, "a@pico.test", "lemonsqueezy", "1"); err != nil {
		t.Fatal(err)
	}
	first, err := testDB.FindFeature(user.ID, "plus")
	if err != nil {
		t.Fatal(err)
	}
	if err := testDB.AddPicoPlusUser(user.Name, "a@pico.test", "lemonsqueezy", "2"); err != nil {
		t.Fatal(err)
	}
	second, err := testDB.FindFeature(user.ID, "plus")
	if err != nil {
		t.Fatal(err)
	}
	if got := second.ExpiresAt.Sub(*first.ExpiresAt); got < 364*24*time.Hour {
		t.Fatalf("renewal added %s, want a year", got)
	}
}
