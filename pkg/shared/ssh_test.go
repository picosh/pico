package shared

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"slices"
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

func TestPubkeyCertVerifyScoped(t *testing.T) {
	ca := newTestSigner(t)

	cases := []struct {
		principals []string
		want       []Scope
		ok         bool
	}{
		{principals: []string{"picd:deploy:blog"}, want: []Scope{{Role: "deploy", Resource: "blog"}}, ok: true},
		{principals: []string{"picd:read", "pgs", "picd:deploy:~pico/a:b"}, want: []Scope{{Role: "read"}, {Role: "deploy", Resource: "~pico/a:b"}}, ok: true},
		{principals: []string{"picd", "picd:deploy:blog"}, ok: true},
		{principals: []string{"picd:deploy:blog", "admin"}, ok: true},
		{principals: []string{"pgs", "pgs:deploy:blog"}},
		{principals: []string{"picdx:deploy:blog"}},
		{principals: []string{"picd:"}},
		{principals: []string{"picd::blog"}},
		{principals: []string{"picd:deploy:"}},
		{principals: []string{"picd:deploy:\x1b"}},
		{principals: []string{"picd:deploy:blog", "picd:"}},
		{},
	}

	for _, c := range cases {
		cert := newTestCert(t, ca, newTestSigner(t).PublicKey(), c.principals)

		authed, err := PubkeyCertVerifyScoped(cert, "picd")
		if (err == nil) != c.ok {
			t.Errorf("PubkeyCertVerifyScoped(%q) err = %v, want ok=%v", c.principals, err, c.ok)
			continue
		}
		if c.ok && (!slices.Equal(authed.Scopes, c.want) || authed.Pubkey != KeyForKeyText(ca.PublicKey())) {
			t.Errorf("PubkeyCertVerifyScoped(%q) = %+v, want scopes %v", c.principals, authed, c.want)
		}

		// Services that don't enforce scopes refuse certs that rely on them.
		if _, err := PubkeyCertVerify(cert, "picd"); c.want != nil && err == nil {
			t.Errorf("PubkeyCertVerify(%q) accepted a scoped cert", c.principals)
		}
	}

	if _, err := PubkeyCertVerifyScoped(newTestCert(t, ca, ca.PublicKey(), []string{":deploy"}), ""); err == nil {
		t.Error("accepted a scoped cert for no service")
	}

	authed, err := PubkeyCertVerifyScoped(ca.PublicKey(), "picd")
	if err != nil || authed.Scopes != nil {
		t.Errorf("plain key = %+v, %v", authed, err)
	}
}

func TestPubkeyCertVerifyScopedChecksCert(t *testing.T) {
	ca := newTestSigner(t)

	sign := func(change func(*ssh.Certificate)) *ssh.Certificate {
		cert := &ssh.Certificate{
			Key:             newTestSigner(t).PublicKey(),
			CertType:        ssh.UserCert,
			ValidPrincipals: []string{"picd:deploy:blog"},
			ValidBefore:     ssh.CertTimeInfinity,
		}
		change(cert)
		if err := cert.SignCert(rand.Reader, ca); err != nil {
			t.Fatal(err)
		}
		return cert
	}

	forged := sign(func(*ssh.Certificate) {})
	forged.SignatureKey = newTestSigner(t).PublicKey()

	widened := sign(func(*ssh.Certificate) {})
	widened.ValidPrincipals = append(widened.ValidPrincipals, "picd:deploy:shop")

	for name, cert := range map[string]*ssh.Certificate{
		"forged":  forged,
		"widened": widened,
		"expired": sign(func(c *ssh.Certificate) { c.ValidBefore = 1 }),
		"host":    sign(func(c *ssh.Certificate) { c.CertType = ssh.HostCert }),
		"force-command": sign(func(c *ssh.Certificate) {
			c.CriticalOptions = map[string]string{"force-command": "ls"}
		}),
	} {
		if _, err := PubkeyCertVerifyScoped(cert, "picd"); err == nil {
			t.Errorf("%s cert was accepted", name)
		}
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

func TestScopesAllows(t *testing.T) {
	scopes := Scopes{{Role: "deploy", Resource: "blog"}, {Role: "read", Resource: "ci-*"}}
	cases := []struct {
		scopes   Scopes
		resource string
		roles    []string
		want     bool
	}{
		{nil, "anything", []string{"deploy"}, true},
		{nil, "", []string{"deploy"}, true},
		{Scopes{}, "blog", []string{"deploy"}, false},
		{scopes, "blog", []string{"deploy"}, true},
		{scopes, "blog", []string{"read"}, false},
		{scopes, "blog", []string{"read", "deploy"}, true},
		{scopes, "Blog", []string{"deploy"}, false},
		{scopes, "blog2", []string{"deploy"}, false},
		{scopes, "", []string{"read", "deploy"}, false},
		{scopes, "ci-1", []string{"read"}, true},
		{scopes, "ci-", []string{"read"}, true},
		{scopes, "ci", []string{"read"}, false},
		{Scopes{{Role: "read", Resource: "*"}}, "x", []string{"read"}, true},
		{Scopes{{Role: "read", Resource: "*"}}, "", []string{"read"}, false},
		{Scopes{{Role: "read"}}, "", []string{"read"}, true},
		{Scopes{{Role: "read"}}, "x", []string{"read"}, true},
	}
	for _, c := range cases {
		if got := c.scopes.Allows(c.resource, c.roles...); got != c.want {
			t.Errorf("%v.Allows(%q, %q) = %v, want %v", c.scopes, c.resource, c.roles, got, c.want)
		}
	}

	if Scopes(nil).CheckFull("x") != nil || (Scopes{{Role: "read"}}).CheckFull("x") == nil {
		t.Error("CheckFull doesn't tell limited connections apart")
	}
	if got := permissionScopes(&ssh.Permissions{Extensions: map[string]string{"scopes": "nonsense"}}); !got.Limited() || len(got) != 0 {
		t.Errorf("unreadable scopes = %#v, want none allowed", got)
	}
}

type asUser struct {
	fakeConn
	user string
}

func (c asUser) User() string { return c.user }

func TestPubkeyAuthHandlerScopes(t *testing.T) {
	ca := newTestSigner(t)
	cases := []struct {
		name       string
		roles      []string
		user       string
		principals []string
		want       string
		ok         bool
	}{
		{name: "scoped", roles: []string{"read", "deploy"}, principals: []string{"pgs:deploy:blog"}, want: `[{"role":"deploy","resource":"blog"}]`, ok: true},
		{name: "full", roles: []string{"read"}, principals: []string{"pgs", "pgs:read:blog"}, ok: true},
		{name: "service without scopes", principals: []string{"pgs:read:blog"}},
		{name: "unknown role", roles: []string{"read"}, principals: []string{"pgs:read:a", "pgs:deploy:b"}},
		{name: "admin session", roles: []string{"read"}, user: "admin__victim", principals: []string{"pgs:read"}},
	}
	for _, c := range cases {
		fdb := &fakeAuthDB{user: &db.User{ID: "1", Name: "erock"}}
		handler := NewSshAuthHandler(fdb, slog.New(slog.DiscardHandler), "pgs", c.roles...)
		cert := newTestCert(t, ca, newTestSigner(t).PublicKey(), c.principals)

		perms, err := handler.PubkeyAuthHandler(asUser{user: c.user}, cert)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", c.name, err, c.ok)
			continue
		}
		if c.ok {
			got, limited := perms.Extensions["scopes"]
			if got != c.want || limited != (c.want != "") {
				t.Errorf("%s: scopes = %q, want %q", c.name, got, c.want)
			}
		}
	}
}
