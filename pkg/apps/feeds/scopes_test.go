package feeds

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/postfs"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"golang.org/x/crypto/ssh"
)

type scopesDB struct{ *stub.PostsDB }

func (d scopesDB) FindPostsByUser(_ *db.Pager, userID, space string) (*db.Paginate[*db.Post], error) {
	posts, err := d.FindAllPostsByUser(userID, space)
	return &db.Paginate[*db.Post]{Data: posts}, err
}

func newSigner(t *testing.T) ssh.Signer {
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

func TestScopedCommands(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	ca := newSigner(t)
	posts := scopesDB{stub.NewPostsDB(logger, &db.User{ID: "user-1", Name: "tester"})}
	posts.Pubkey = shared.KeyForKeyText(ca.PublicKey())
	now := time.Now()
	for _, name := range []string{"news.txt", "other.txt"} {
		if _, err := posts.InsertPost(&db.Post{UserID: "user-1", Space: "feeds", Filename: name, Slug: name, Text: "text of " + name, PublishAt: &now, UpdatedAt: &now}); err != nil {
			t.Fatal(err)
		}
	}

	cfg := NewConfig("feeds-test")
	cfg.Logger = logger
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	auth := shared.NewSshAuthHandler(posts, logger, "feeds", postfs.RoleRead, postfs.RoleWrite)
	server, err := pssh.NewSSHServerWithConfig(ctx, logger, "feeds", "127.0.0.1", "0", "", filepath.Join(t.TempDir(), "host_key"),
		auth.PubkeyAuthHandler,
		[]pssh.SSHServerMiddleware{Middleware(posts, cfg), pssh.LogMiddleware(NewUploadHandler(cfg, posts), posts)}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.ListenAndServe() }()
	addr := ""
	for range 100 {
		server.Mu.Lock()
		if server.Listener != nil {
			addr = server.Listener.Addr().String()
		}
		server.Mu.Unlock()
		if addr != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	run := func(t *testing.T, principal, command string) (string, bool) {
		t.Helper()
		key := newSigner(t)
		cert := &ssh.Certificate{Key: key.PublicKey(), CertType: ssh.UserCert, ValidPrincipals: []string{principal}, ValidBefore: ssh.CertTimeInfinity}
		if err := cert.SignCert(rand.Reader, ca); err != nil {
			t.Fatal(err)
		}
		signer, err := ssh.NewCertSigner(cert, key)
		if err != nil {
			t.Fatal(err)
		}
		client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: "ci", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey()})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		session, err := client.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		out, err := session.CombinedOutput(command)
		var exit *ssh.ExitError
		if err != nil && !errors.As(err, &exit) {
			var missing *ssh.ExitMissingError
			if !errors.As(err, &missing) {
				t.Fatal(err)
			}
		}
		return string(out), !strings.Contains(string(out), "this certificate doesn't allow")
	}

	cases := []struct {
		name, principal, command string
		ok                       bool
		want, absent             string
	}{
		{"ls", "feeds:read:news.txt", "ls", true, "news.txt", "other.txt"},
		{"print", "feeds:read:news.txt", "print news.txt", true, "text of news.txt", ""},
		{"print another", "feeds:read:news.txt", "print other.txt", false, "", "text of"},
		{"run by a reader", "feeds:read:news.txt", "run news.txt", false, "", "running feed post"},
		{"run another", "feeds:write:news.txt", "run other.txt", false, "", "running feed post"},
		{"rm by a reader", "feeds:read", "rm other.txt --write", false, "", "post removed"},
		{"rm another", "feeds:write:news.txt", "rm other.txt --write", false, "", "post removed"},
		{"rm", "feeds:write:news.txt", "rm news.txt --write", true, "removed", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ok := run(t, c.principal, c.command)
			if ok != c.ok || !strings.Contains(out, c.want) || (c.absent != "" && strings.Contains(out, c.absent)) {
				t.Errorf("%q, want ok = %v and %q without %q", out, c.ok, c.want, c.absent)
			}
		})
	}

	if _, err := posts.FindPostWithFilename("other.txt", "user-1", "feeds"); err != nil {
		t.Errorf("other.txt was removed: %v", err)
	}
	if _, err := posts.FindPostWithFilename("news.txt", "user-1", "feeds"); err == nil {
		t.Error("news.txt wasn't removed")
	}
}
