package pico

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
	"golang.org/x/crypto/ssh"
)

type fakeSession struct{ bytes.Buffer }

func (s *fakeSession) Exit(int) error        { return nil }
func (s *fakeSession) Close() error          { return nil }
func (s *fakeSession) Stderr() io.ReadWriter { return &s.Buffer }

type dnsCheckDB struct {
	db.DB
	users    []*db.User
	projects map[string]bool
	keys     []*db.PublicKey
}

func (d *dnsCheckDB) FindKeysByUser(user *db.User) ([]*db.PublicKey, error) {
	keys := []*db.PublicKey{}
	for _, k := range d.keys {
		if k.UserID == user.ID {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (d *dnsCheckDB) FindUserByName(name string) (*db.User, error) {
	for _, u := range d.users {
		if u.Name == name {
			return u, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

func (d *dnsCheckDB) FindProjectByName(userID, name string) (*db.Project, error) {
	if d.projects[userID+"/"+name] {
		return &db.Project{UserID: userID, Name: name}, nil
	}
	return nil, fmt.Errorf("project not found")
}

func TestDNSCheck(t *testing.T) {
	me := &db.User{ID: "1", Name: "antonio"}
	other := &db.User{ID: "2", Name: "erock"}
	edKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ssh.NewPublicKey(edKey)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := ssh.FingerprintSHA256(pk)
	dbpool := &dnsCheckDB{
		users:    []*db.User{me, other},
		projects: map[string]bool{"1/blog": true},
		keys:     []*db.PublicKey{{UserID: me.ID, Name: "laptop", Key: shared.KeyForKeyText(pk)}},
	}

	tests := []struct {
		name    string
		domain  string
		records map[string][]string
		want    []string
	}{
		{"no records", "", map[string][]string{}, []string{
			"_pgs.example.com: no TXT record",
			"_prose.example.com: no TXT record",
			"_tuns.example.com: no TXT record",
			"_sish.example.com: no TXT record",
		}},
		{"valid", "", map[string][]string{
			"pgs":   {"antonio-blog"},
			"prose": {"erock"},
			"sish":  {"SHA256:someone-else", fingerprint},
		}, []string{
			`_pgs.example.com: "antonio-blog" is valid, serving pgs project "blog" of antonio`,
			`_prose.example.com: "erock" is valid, serving prose blog of erock (not your account)`,
			fmt.Sprintf(`_sish.example.com: %q is valid, tunnels from your key "laptop" can use this domain`, fingerprint),
		}},
		{"invalid", "", map[string][]string{
			"pgs":   {"antonio-blgo"},
			"prose": {"nobody"},
			"sish":  {"SHA256:someone-else"},
		}, []string{
			`_pgs.example.com: "antonio-blgo" is invalid: user "antonio" has no project "blgo"`,
			`_prose.example.com: "nobody" is invalid: user "nobody" not found`,
			`_sish.example.com: "SHA256:someone-else" is invalid: no record is your username or the SHA256 fingerprint of one of your keys`,
		}},
		{"tuns record with whitespace", "", map[string][]string{"sish": {fingerprint + " "}}, []string{
			fmt.Sprintf(`_sish.example.com: %q is invalid: it has surrounding whitespace, and tuns needs an exact match`, fingerprint+" "),
		}},
		{"tuns username", "", map[string][]string{"tuns": {"antonio"}}, []string{
			`_tuns.example.com: "antonio" is valid, tunnels from any of your keys can use this domain`,
			"_sish.example.com: no TXT record",
		}},
		{"wildcard", "*.example.com", map[string][]string{"pgs": {"antonio-blog"}, "sish": {"antonio"}}, []string{
			"_pgs.*.example.com: wildcard domains are not supported\r\n",
			"_prose.*.example.com: wildcard domains are not supported\r\n",
			`.example.com: "antonio" is valid, tunnels from any of your keys can use this domain`,
			"_tuns.",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &fakeSession{}
			cmd := &Cmd{
				User:    me,
				Session: session,
				Log:     slog.New(slog.DiscardHandler),
				Dbpool:  dbpool,
				lookupTXT: func(host, space string) ([]string, error) {
					if tt.domain == "" && host != "example.com" {
						t.Fatalf("looked up %q", host)
					}
					if tt.domain != "" && (!strings.HasSuffix(host, ".example.com") || strings.Contains(host, "*")) {
						t.Fatalf("looked up %q for %q", host, tt.domain)
					}
					if v, ok := tt.records[space]; ok {
						return v, nil
					}
					return nil, router.ErrNoTXTRecord
				},
			}
			domain := tt.domain
			if domain == "" {
				domain = " Example.COM. "
			}
			if err := cmd.dnsCheck(domain); err != nil {
				t.Fatal(err)
			}
			out := session.String()
			for _, line := range tt.want {
				if !strings.Contains(out, line) {
					t.Errorf("missing %q in output:\n%s", line, out)
				}
			}
		})
	}

	cmd := &Cmd{User: me, Session: &fakeSession{}, Dbpool: dbpool}
	for _, bad := range []string{"https://example.com/", "*.", "a.*.example.com"} {
		if err := cmd.dnsCheck(bad); err == nil {
			t.Errorf("%q was accepted as a domain", bad)
		}
	}
}
