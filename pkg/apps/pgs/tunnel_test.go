package pgs

import (
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"testing"
	"time"

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/db"
	"golang.org/x/crypto/ssh"
)

func TestTunnelRequesterImpersonation(t *testing.T) {
	logger := slog.Default()
	dbpool := pgsdb.NewDBMemory(logger)
	attacker := &db.User{ID: "attacker-id", Name: "attacker"}
	admin := &db.User{ID: "admin-id", Name: "admin"}
	victim := &db.User{ID: "victim-id", Name: "victim"}
	dbpool.Users = append(dbpool.Users, attacker, admin, victim)
	dbpool.Pubkeys = append(dbpool.Pubkeys,
		&db.PublicKey{UserID: attacker.ID, Key: "attacker-key"},
		&db.PublicKey{UserID: admin.ID, Key: "admin-key"},
	)
	expired := time.Now().Add(-time.Hour)
	valid := time.Now().Add(time.Hour)
	dbpool.Features = append(dbpool.Features,
		&db.FeatureFlag{UserID: attacker.ID, Name: "admin", ExpiresAt: &expired},
		&db.FeatureFlag{UserID: admin.ID, Name: "admin", ExpiresAt: &valid},
	)
	cfg := &Config{DB: dbpool, Logger: logger}

	if got, err := tunnelRequester(cfg, "attacker-key", ""); err != nil || got.ID != attacker.ID {
		t.Fatalf("own tunnel = %v, %v", got, err)
	}
	if got, err := tunnelRequester(cfg, "attacker-key", victim.Name); err == nil {
		t.Fatalf("expired admin impersonated %s", got.Name)
	}
	if got, err := tunnelRequester(cfg, "admin-key", victim.Name); err != nil || got.ID != victim.ID {
		t.Fatalf("admin impersonation = %v, %v", got, err)
	}
	if _, err := tunnelRequester(cfg, "admin-key", "nobody"); err == nil {
		t.Fatal("impersonating a missing user succeeded")
	}
	if _, err := tunnelRequester(cfg, "unknown-key", ""); err == nil {
		t.Fatal("unknown key succeeded")
	}
}

func TestHasProjectAccess(t *testing.T) {
	owner := &db.User{ID: "owner", Name: "owner"}
	other := &db.User{ID: "other", Name: "other"}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	fp := ssh.FingerprintSHA256(key)

	tt := []struct {
		name      string
		acl       db.ProjectAcl
		requester *db.User
		pubkey    ssh.PublicKey
		want      bool
	}{
		{"public", db.ProjectAcl{Type: "public"}, nil, nil, true},
		{"empty type", db.ProjectAcl{}, nil, nil, true},
		{"private owner", db.ProjectAcl{Type: "private"}, owner, key, true},
		{"private other", db.ProjectAcl{Type: "private"}, other, key, false},
		{"pico any user", db.ProjectAcl{Type: "pico"}, other, key, true},
		{"pico listed", db.ProjectAcl{Type: "pico", Data: []string{"other"}}, other, key, true},
		{"pico unlisted", db.ProjectAcl{Type: "pico", Data: []string{"someone"}}, other, key, false},
		{"pubkeys listed", db.ProjectAcl{Type: "pubkeys", Data: []string{fp}}, other, key, true},
		{"pubkeys unlisted", db.ProjectAcl{Type: "pubkeys", Data: []string{"SHA256:x"}}, other, key, false},
		{"pubkeys no key", db.ProjectAcl{Type: "pubkeys"}, other, nil, false},
		{"http-pass other", db.ProjectAcl{Type: "http-pass", Data: []string{"pw"}}, other, key, false},
		{"unknown type", db.ProjectAcl{Type: "bogus"}, other, key, false},
	}
	for _, tc := range tt {
		project := &db.Project{Acl: tc.acl}
		if got := HasProjectAccess(project, owner, tc.requester, tc.pubkey); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
