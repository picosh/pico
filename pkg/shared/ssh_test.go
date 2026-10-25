package shared

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"testing"

	"github.com/picosh/pico/pkg/db"
	"golang.org/x/crypto/ssh"
)

type fakeAuthDB struct {
	user *db.User
	logs []*db.AccessLog
}

func (f *fakeAuthDB) FindUser(string) (*db.User, error)                   { return f.user, nil }
func (f *fakeAuthDB) FindUserByPubkey(string) (*db.User, error)           { return f.user, nil }
func (f *fakeAuthDB) FindUserByName(string) (*db.User, error)             { return f.user, nil }
func (f *fakeAuthDB) FindFeature(string, string) (*db.FeatureFlag, error) { return nil, nil }
func (f *fakeAuthDB) InsertAccessLog(log *db.AccessLog) error {
	f.logs = append(f.logs, log)
	return nil
}

type fakeConn struct{ ssh.ConnMetadata }

func (fakeConn) User() string { return "erock" }

func TestPubkeyAuthHandlerUsesKeyName(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	fdb := &fakeAuthDB{user: &db.User{ID: "1", Name: "erock", PublicKey: &db.PublicKey{Name: "laptop"}}}
	handler := NewSshAuthHandler(fdb, slog.New(slog.NewTextHandler(io.Discard, nil)), "pgs")

	perms, err := handler.PubkeyAuthHandler(fakeConn{}, key)
	if err != nil {
		t.Fatal(err)
	}
	if got := perms.Extensions["identity"]; got != "laptop" {
		t.Fatalf("identity = %q, want laptop", got)
	}
	if len(fdb.logs) != 1 || fdb.logs[0].Identity != "laptop" {
		t.Fatalf("access log identity = %+v, want laptop", fdb.logs)
	}
}
