package shared

import (
	"crypto/rand"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"golang.org/x/crypto/ssh"
)

func newIDCert(t *testing.T, ca ssh.Signer, key ssh.PublicKey, keyID string, principals ...string) *ssh.Certificate {
	t.Helper()
	cert := &ssh.Certificate{Key: key, CertType: ssh.UserCert, KeyId: keyID, ValidPrincipals: principals, ValidBefore: ssh.CertTimeInfinity}
	if err := cert.SignCert(rand.Reader, ca); err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestPubkeyAuthHandlerRevoked(t *testing.T) {
	ca, key := newTestSigner(t), newTestSigner(t)
	fp := ssh.FingerprintSHA256(key.PublicKey())
	entry := func(userID, kind, value string) []db.CertRevocation {
		return []db.CertRevocation{{UserID: userID, Kind: kind, Value: value}}
	}
	lookupErr := errors.New("no table")

	cases := []struct {
		name    string
		revoked []db.CertRevocation
		err     error
		refused bool
	}{
		{name: "no entries"},
		{name: "key", revoked: entry("1", db.CertRevocationKey, fp), refused: true},
		{name: "id", revoked: entry("1", db.CertRevocationID, "ci"), refused: true},
		{name: "the CA's key", revoked: entry("1", db.CertRevocationKey, ssh.FingerprintSHA256(ca.PublicKey()))},
		{name: "another id", revoked: entry("1", db.CertRevocationID, "c")},
		{name: "key as an id", revoked: entry("1", db.CertRevocationID, fp)},
		{name: "another account's key", revoked: entry("2", db.CertRevocationKey, fp)},
		{name: "another account's id", revoked: entry("2", db.CertRevocationID, "ci")},
		{name: "lookup fails", err: lookupErr, refused: true},
	}

	for _, c := range cases {
		for _, principal := range []string{"admin", "pgs", "pgs:deploy:blog"} {
			for _, open := range []bool{false, true} {
				fdb := &fakeAuthDB{user: &db.User{ID: "1", Name: "erock"}, revoked: c.revoked, revokedErr: c.err}
				handler := NewSshAuthHandler(fdb, slog.New(slog.DiscardHandler), "pgs", "deploy")
				auth := handler.PubkeyAuthHandler
				if open {
					auth = handler.OpenPubkeyAuthHandler
				}

				perms, err := auth(userConn{user: "erock"}, newIDCert(t, ca, key.PublicKey(), "ci", principal))
				if (err != nil) != c.refused || (perms == nil) != c.refused {
					t.Errorf("%s: %s cert, open = %v: %v, %v, want refused = %v", c.name, principal, open, perms, err, c.refused)
				}
				if c.err != nil && !errors.Is(err, c.err) || c.err == nil && c.refused && !errors.Is(err, ErrCertRevoked) {
					t.Errorf("%s: err = %v", c.name, err)
				}
				if c.refused == (len(fdb.logs) != 0) {
					t.Errorf("%s: %d access logs, refused = %v", c.name, len(fdb.logs), c.refused)
				}

				// Entries don't reach plain keys.
				if _, err := auth(userConn{user: "erock"}, key.PublicKey()); err != nil {
					t.Errorf("%s: plain key: %v", c.name, err)
				}
			}
		}
	}
}

// An admin's certs are revoked by the admin's account, whoever they act as.
func TestPubkeyAuthHandlerRevokedImpersonation(t *testing.T) {
	ca := newTestSigner(t)
	root, alice := &db.User{ID: "root-id", Name: "root"}, &db.User{ID: "alice-id", Name: "alice"}
	fdb := &impersonationDB{
		users:    map[string]*db.User{"root": root, "alice": alice},
		byPubkey: map[string]*db.User{KeyForKeyText(ca.PublicKey()): root},
		admins:   map[string]bool{"root-id": true},
	}
	cert := newIDCert(t, ca, newTestSigner(t).PublicKey(), "ci", "admin")
	handler := NewSshAuthHandler(fdb, slog.New(slog.DiscardHandler), "pgs")

	fdb.revoked = []db.CertRevocation{{UserID: "alice-id", Kind: db.CertRevocationID, Value: "ci"}}
	if perms, err := handler.PubkeyAuthHandler(userConn{user: "admin__alice"}, cert); err != nil || perms.Extensions["user_id"] != "alice-id" {
		t.Errorf("revoked by the impersonated account: %v, %v", perms, err)
	}
	fdb.revoked = []db.CertRevocation{{UserID: "root-id", Kind: db.CertRevocationID, Value: "ci"}}
	if _, err := handler.PubkeyAuthHandler(userConn{user: "admin__alice"}, cert); !errors.Is(err, ErrCertRevoked) {
		t.Errorf("revoked by the admin: %v", err)
	}
}

// Without an account, a key is only let in as a plain key.
func TestOpenPubkeyAuthHandler(t *testing.T) {
	ca, key := newTestSigner(t), newTestSigner(t)
	fdb := &impersonationDB{byPubkey: map[string]*db.User{KeyForKeyText(ca.PublicKey()): {ID: "1", Name: "erock"}}}
	fdb.revoked = []db.CertRevocation{{UserID: "1", Kind: db.CertRevocationID, Value: "ci"}}
	handler := NewSshAuthHandler(fdb, slog.New(slog.DiscardHandler), "pico")

	perms, err := handler.OpenPubkeyAuthHandler(userConn{user: "erock"}, key.PublicKey())
	if err != nil || len(perms.Extensions) != 1 || perms.Extensions["pubkey"] != KeyForKeyText(key.PublicKey()) {
		t.Errorf("unknown key: %v, %v", perms, err)
	}
	for name, cert := range map[string]*ssh.Certificate{
		"revoked":    newIDCert(t, ca, key.PublicKey(), "ci", "pico"),
		"scoped":     newIDCert(t, ca, key.PublicKey(), "ok", "pico:read"),
		"unknown CA": newIDCert(t, key, key.PublicKey(), "ok", "pico"),
	} {
		if perms, err := handler.OpenPubkeyAuthHandler(userConn{user: "erock"}, cert); err == nil {
			t.Errorf("%s cert was accepted: %v", name, perms)
		}
	}
}

// refusingDB fails as postgres does on text it can't hold.
type refusingDB struct{ fakeAuthDB }

func (f *refusingDB) IsCertRevoked(userID, fingerprint, keyID string) (bool, error) {
	if keyID != "" && !RevocableKeyID(keyID) {
		return false, errors.New("invalid byte sequence")
	}
	return f.fakeAuthDB.IsCertRevoked(userID, fingerprint, keyID)
}

func TestUnusualKeyIDs(t *testing.T) {
	ca, key := newTestSigner(t), newTestSigner(t)
	for _, id := range []string{"", "a\x00b", "\xff\xfe", strings.Repeat("x", 1025)} {
		fdb := &refusingDB{fakeAuthDB{user: &db.User{ID: "1", Name: "erock"}}}
		handler := NewSshAuthHandler(fdb, slog.New(slog.DiscardHandler), "pgs")
		cert := newIDCert(t, ca, key.PublicKey(), id, "pgs")
		if _, err := handler.PubkeyAuthHandler(fakeConn{}, cert); err != nil {
			t.Errorf("%.10q: %v", id, err)
		}
		fdb.revoked = []db.CertRevocation{{UserID: "1", Kind: db.CertRevocationKey, Value: ssh.FingerprintSHA256(key.PublicKey())}}
		if _, err := handler.PubkeyAuthHandler(fakeConn{}, cert); !errors.Is(err, ErrCertRevoked) {
			t.Errorf("%.10q revoked by key: %v", id, err)
		}
	}
}

func TestParseCert(t *testing.T) {
	ca, key := newTestSigner(t), newTestSigner(t)
	ok := newIDCert(t, ca, key.PublicKey(), "ci", "pgs:read:blog", "pgs")
	forged := newIDCert(t, newTestSigner(t), key.PublicKey(), "ci", "pgs")
	forged.SignatureKey = ca.PublicKey()
	renamed := newIDCert(t, ca, key.PublicKey(), "ci", "pgs")
	renamed.KeyId = "other"
	signed := func(cert *ssh.Certificate) *ssh.Certificate {
		cert.Key, cert.KeyId, cert.ValidPrincipals = key.PublicKey(), "ci", []string{"pgs"}
		if err := cert.SignCert(rand.Reader, ca); err != nil {
			t.Fatal(err)
		}
		return cert
	}
	future := uint64(time.Now().Add(time.Hour).Unix())
	expired := signed(&ssh.Certificate{CertType: ssh.UserCert, ValidBefore: 1})
	forgedExpired := *expired
	forgedExpired.ValidPrincipals = []string{"admin"}

	for name, cert := range map[string]*ssh.Certificate{
		"current":       ok,
		"expired":       expired,
		"not valid yet": signed(&ssh.Certificate{CertType: ssh.UserCert, ValidAfter: future, ValidBefore: ssh.CertTimeInfinity}),
	} {
		if got, err := ParseCert(KeyForKeyText(cert)); err != nil || got.KeyId != "ci" {
			t.Errorf("%s: ParseCert = %v, %v", name, got, err)
		}
	}
	for name, line := range map[string]string{
		"forged":          KeyForKeyText(forged),
		"renamed":         KeyForKeyText(renamed),
		"forged expired":  KeyForKeyText(&forgedExpired),
		"never valid":     KeyForKeyText(signed(&ssh.Certificate{CertType: ssh.UserCert, ValidAfter: 2, ValidBefore: 1})),
		"host":            KeyForKeyText(signed(&ssh.Certificate{CertType: ssh.HostCert, ValidBefore: ssh.CertTimeInfinity})),
		"critical option": KeyForKeyText(signed(&ssh.Certificate{CertType: ssh.UserCert, ValidBefore: ssh.CertTimeInfinity, Permissions: ssh.Permissions{CriticalOptions: map[string]string{"force-command": "x"}}})),
		"no principals":   KeyForKeyText(newIDCert(t, ca, key.PublicKey(), "ci")),
		"plain key":       KeyForKeyText(key.PublicKey()),
		"nonsense":        "ssh-ed25519-cert-v01@openssh.com AAAA",
		"empty":           "",
	} {
		if _, err := ParseCert(line); err == nil {
			t.Errorf("%s cert was parsed", name)
		}
	}
}
