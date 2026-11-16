package shared

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

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

func newTestCert(t *testing.T, ca ssh.Signer, key ssh.PublicKey, principals []string) *ssh.Certificate {
	t.Helper()
	cert := &ssh.Certificate{
		Key:             key,
		CertType:        ssh.UserCert,
		ValidPrincipals: principals,
		ValidBefore:     ssh.CertTimeInfinity,
	}
	if err := cert.SignCert(rand.Reader, ca); err != nil {
		t.Fatal(err)
	}
	return cert
}

func newTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestPubkeyCertVerifyAcceptsSignedCert(t *testing.T) {
	ca := newTestSigner(t)
	user := newTestSigner(t)
	cert := newTestCert(t, ca, user.PublicKey(), []string{"pgs"})

	authed, err := PubkeyCertVerify(cert, "pgs")
	if err != nil {
		t.Fatal(err)
	}
	if authed.Pubkey != KeyForKeyText(ca.PublicKey()) {
		t.Fatalf("pubkey = %q, want the CA key", authed.Pubkey)
	}
}

// A cert naming someone else's key as its CA, without their signature, must
// not authenticate as them.
func TestPubkeyCertVerifyRejectsForgedCert(t *testing.T) {
	victim := newTestSigner(t)
	attacker := newTestSigner(t)
	cert := newTestCert(t, attacker, attacker.PublicKey(), []string{"admin"})
	cert.SignatureKey = victim.PublicKey()

	if _, err := PubkeyCertVerify(cert, "pgs"); err == nil {
		t.Fatal("forged cert was accepted")
	}
}

func TestPubkeyCertVerifyRejectsOtherPrincipal(t *testing.T) {
	ca := newTestSigner(t)
	cert := newTestCert(t, ca, newTestSigner(t).PublicKey(), []string{"prose"})

	if _, err := PubkeyCertVerify(cert, "pgs"); err == nil {
		t.Fatal("cert for another service was accepted")
	}
}

func TestPubkeyCertVerifyRejectsUnknownCriticalOption(t *testing.T) {
	ca := newTestSigner(t)
	cert := &ssh.Certificate{
		Key:             newTestSigner(t).PublicKey(),
		CertType:        ssh.UserCert,
		ValidPrincipals: []string{"pgs"},
		ValidBefore:     ssh.CertTimeInfinity,
		Permissions:     ssh.Permissions{CriticalOptions: map[string]string{"force-command": "ls"}},
	}
	if err := cert.SignCert(rand.Reader, ca); err != nil {
		t.Fatal(err)
	}

	if _, err := PubkeyCertVerify(cert, "pgs"); err == nil {
		t.Fatal("cert with an unsupported critical option was accepted")
	}
}

func TestCheckSourceAddress(t *testing.T) {
	authed := &AuthedPubkey{CriticalOptions: map[string]string{"source-address": "10.0.0.0/8,192.168.1.5"}}
	for addr, ok := range map[string]bool{
		"10.1.2.3:22":    true,
		"192.168.1.5":    true,
		"192.168.1.6:22": false,
		"":               false,
	} {
		if err := authed.CheckSourceAddress(addr); (err == nil) != ok {
			t.Errorf("CheckSourceAddress(%q) err = %v, want ok=%v", addr, err, ok)
		}
	}

	if err := (&AuthedPubkey{}).CheckSourceAddress(""); err != nil {
		t.Errorf("no restriction: %v", err)
	}
}

type impersonationDB struct {
	fakeAuthDB
	users    map[string]*db.User
	byPubkey map[string]*db.User
	admins   map[string]bool
}

func (f *impersonationDB) FindUserByPubkey(key string) (*db.User, error) {
	if u, ok := f.byPubkey[key]; ok {
		return u, nil
	}
	return nil, fmt.Errorf("no user for key")
}

func (f *impersonationDB) FindUserByName(name string) (*db.User, error) {
	if u, ok := f.users[name]; ok {
		return u, nil
	}
	return nil, fmt.Errorf("no user %s", name)
}

func (f *impersonationDB) FindFeature(userID, name string) (*db.FeatureFlag, error) {
	if name == "admin" && f.admins[userID] {
		expires := time.Now().Add(time.Hour)
		return &db.FeatureFlag{UserID: userID, Name: name, ExpiresAt: &expires}, nil
	}
	return nil, fmt.Errorf("no feature")
}

type userConn struct {
	ssh.ConnMetadata
	user string
}

func (c userConn) User() string { return c.user }

func TestPubkeyAuthHandlerImpersonation(t *testing.T) {
	adminKey := newTestSigner(t)
	plainKey := newTestSigner(t)
	ca := newTestSigner(t)
	root := &db.User{ID: "root-id", Name: "root"}
	bob := &db.User{ID: "bob-id", Name: "bob"}
	alice := &db.User{ID: "alice-id", Name: "alice"}
	fdb := &impersonationDB{
		users: map[string]*db.User{"root": root, "bob": bob, "alice": alice},
		byPubkey: map[string]*db.User{
			KeyForKeyText(adminKey.PublicKey()): root,
			KeyForKeyText(ca.PublicKey()):       root,
			KeyForKeyText(plainKey.PublicKey()): bob,
		},
		admins: map[string]bool{"root-id": true},
	}
	adminCert := newTestCert(t, ca, newTestSigner(t).PublicKey(), []string{"admin"})

	for _, tt := range []struct {
		name   string
		key    ssh.PublicKey
		user   string
		wantID string
		impID  string
	}{
		{"admin key", adminKey.PublicKey(), "admin__alice", "alice-id", "root-id"},
		{"admin cert", adminCert, "admin__alice", "alice-id", "root-id"},
		{"admin as self", adminKey.PublicKey(), "root", "root-id", ""},
		{"non-admin", plainKey.PublicKey(), "admin__alice", "bob-id", ""},
		{"unknown user", adminKey.PublicKey(), "admin__nobody", "root-id", ""},
	} {
		for _, service := range []string{"pico", "pgs", "prose", "pastes", "feeds", "pipe"} {
			handler := NewSshAuthHandler(fdb, slog.New(slog.DiscardHandler), service)
			perms, err := handler.PubkeyAuthHandler(userConn{user: tt.user}, tt.key)
			if err != nil {
				t.Fatalf("%s %s: %v", tt.name, service, err)
			}
			if perms.Extensions["user_id"] != tt.wantID || perms.Extensions["imp_id"] != tt.impID {
				t.Errorf("%s %s: user_id %q imp_id %q, want %q %q", tt.name, service,
					perms.Extensions["user_id"], perms.Extensions["imp_id"], tt.wantID, tt.impID)
			}
		}
	}
}
