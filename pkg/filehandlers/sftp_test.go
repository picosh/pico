package filehandlers_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/apps/pastes"
	"github.com/picosh/pico/pkg/apps/prose"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/list"
	"github.com/picosh/pico/pkg/send/protocols/sftp"
	"github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
	"golang.org/x/crypto/ssh"
)

type postsEnv struct {
	t     *testing.T
	db    *stub.PostsDB
	space string
	port  string
	key   string
}

func newPostsEnv(t *testing.T, app string) *postsEnv {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	_, userKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(userKey)
	if err != nil {
		t.Fatal(err)
	}
	posts := stub.NewPostsDB(logger, &db.User{ID: "user-1", Name: "tester"})
	posts.Pubkey = shared.KeyForKeyText(signer.PublicKey())

	var space string
	var handler interface {
		utils.CopyFromClientHandler
		pssh.GetLoggerInterface
	}
	switch app {
	case "prose":
		cfg := prose.NewConfig("prose-test")
		cfg.Logger = logger
		st, err := storage.NewStorageMemory(map[string]map[string]string{})
		if err != nil {
			t.Fatal(err)
		}
		handler = prose.NewFileHandler(cfg, posts, st)
		space = cfg.Space
	case "pastes":
		cfg := pastes.NewConfig("pastes-test")
		cfg.Logger = logger
		handler = pastes.NewUploadHandler(cfg, posts)
		space = cfg.Space
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	auth := shared.NewSshAuthHandler(posts, logger, app)
	server, err := pssh.NewSSHServerWithConfig(ctx, logger, app, "127.0.0.1", "0", "", filepath.Join(t.TempDir(), "host_key"),
		auth.PubkeyAuthHandler,
		[]pssh.SSHServerMiddleware{list.Middleware(handler), pssh.LogMiddleware(handler, posts)},
		[]pssh.SSHServerMiddleware{sftp.Middleware(handler), pssh.LogMiddleware(handler, posts)},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.ListenAndServe() }()
	var addr string
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
	if addr == "" {
		t.Fatal("server listener not ready")
	}
	_, port, _ := net.SplitHostPort(addr)

	b, err := ssh.MarshalPrivateKey(userKey, "")
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(key, pem.EncodeToMemory(b), 0o600); err != nil {
		t.Fatal(err)
	}
	return &postsEnv{t: t, db: posts, space: space, port: port, key: key}
}

func (e *postsEnv) sshOpts() []string {
	return []string{"-o", "IdentityFile=" + e.key, "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR"}
}

func (e *postsEnv) run(name string, args ...string) string {
	e.t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		e.t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

// seed stores a hidden post, made hidden the way each service does it.
func (e *postsEnv) seed(filename string) *db.Post {
	e.t.Helper()
	published := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	post := &db.Post{
		UserID: "user-1", Space: e.space, Filename: filename, Slug: strings.TrimSuffix(filename, filepath.Ext(filename)),
		Text: seedText(e.space), FileSize: len(seedText(e.space)), Hidden: true, PublishAt: &published, UpdatedAt: &published,
	}
	if e.space == "pastes" {
		expires := time.Now().Add(time.Hour).Truncate(time.Second)
		post.ExpiresAt = &expires
	}
	p, err := e.db.InsertPost(post)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func seedText(space string) string {
	if space == "prose" {
		return "---\ndraft: true\n---\nseeded\n"
	}
	return "seeded\n"
}

// assertKept waits for the post at filename to have text and checks it is
// the seeded one with its settings. sshfs sends a close after the command
// that made it returns.
func (e *postsEnv) assertKept(filename, text string, seeded *db.Post) {
	e.t.Helper()
	e.assertMoved(filename, text, seeded, seeded.Hidden)
}

// assertMoved is assertKept for a post whose new name changes whether it
// is hidden.
func (e *postsEnv) assertMoved(filename, text string, seeded *db.Post, hidden bool) {
	e.t.Helper()
	var got *db.Post
	for range 100 {
		var err error
		if got, err = e.db.FindPostWithFilename(filename, "user-1", e.space); err == nil && got.Text == text {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got == nil || got.Text != text {
		e.t.Fatalf("%s = %+v, want text %q", filename, got, text)
	}
	if got.ID != seeded.ID || !got.PublishAt.Equal(*seeded.PublishAt) || got.Hidden != hidden ||
		(got.ExpiresAt == nil) != (seeded.ExpiresAt == nil) || (got.ExpiresAt != nil && !got.ExpiresAt.Equal(*seeded.ExpiresAt)) {
		e.t.Errorf("%s = id %s published %v hidden %v expires %v, want %s %v %v %v", filename,
			got.ID, got.PublishAt, got.Hidden, got.ExpiresAt, seeded.ID, seeded.PublishAt, hidden, seeded.ExpiresAt)
	}
}

func assertListed(t *testing.T, out string, want ...string) {
	t.Helper()
	var got []string
	for _, line := range strings.Split(out, "\n") {
		f := filepath.Base(strings.TrimSpace(line))
		if !strings.HasPrefix(line, "sftp>") && f != "." && f != ".." && f != "/" {
			got = append(got, f)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("listed %q, want %q", got, want)
	}
}

func TestPostsSftp(t *testing.T) {
	if _, err := exec.LookPath("sftp"); err != nil {
		t.Skip("sftp not installed")
	}
	for app, ext := range map[string]string{"prose": ".md", "pastes": ".txt"} {
		t.Run(app, func(t *testing.T) {
			e := newPostsEnv(t, app)
			a := e.seed("a" + ext)
			e.seed("c" + ext)

			sftpCmd := func(cmds string) string {
				cmd := exec.Command("sftp", append(append([]string{"-b", "-", "-P", e.port}, e.sshOpts()...), "localhost")...)
				cmd.Stdin = strings.NewReader(cmds)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("sftp: %v\n%s", err, out)
				}
				return string(out)
			}
			assertListed(t, sftpCmd("ls -1\n"), "a"+ext, "c"+ext)
			assertListed(t, e.run("ssh", append(append([]string{"-p", e.port}, e.sshOpts()...), "localhost", "command", "ls")...), "a"+ext, "c"+ext)

			sftpCmd("rename a" + ext + " b" + ext + "\n")
			e.assertKept("b"+ext, a.Text, a)
			sftpCmd("rename b" + ext + " c" + ext + "\n")
			e.assertKept("c"+ext, a.Text, a)
			assertListed(t, sftpCmd("ls -1\n"), "c"+ext)
		})
	}
}

func TestPostsSshfs(t *testing.T) {
	if _, err := exec.LookPath("sshfs"); err != nil {
		t.Skip("sshfs not installed")
	}
	tests := map[string][]struct {
		cmd  string
		name string
		text string
		// public is set when the new name drops the draft setting, which
		// .lxt files don't read from front matter.
		public bool
	}{
		"prose": {
			{cmd: "echo more >> a.md", name: "a.md", text: seedText("prose") + "more\n"},
			{cmd: "mv a.md b.md", name: "b.md", text: seedText("prose") + "more\n"},
			{cmd: "mv b.md b.lxt", name: "b.lxt", text: seedText("prose") + "more\n", public: true},
			{cmd: "mv b.lxt c.md", name: "c.md", text: seedText("prose") + "more\n"},
		},
		"pastes": {
			{cmd: "echo more >> a.txt", name: "a.txt", text: "seeded\nmore\n"},
			{cmd: "vim -u DEFAULTS -i NONE -N -es -c '%s/more/less/' -c wq a.txt", name: "a.txt", text: "seeded\nless\n"},
			{cmd: "mv a.txt b.txt", name: "b.txt", text: "seeded\nless\n"},
			{cmd: "mv b.txt c.txt", name: "c.txt", text: "seeded\nless\n"},
		},
	}
	for app, steps := range tests {
		t.Run(app, func(t *testing.T) {
			e := newPostsEnv(t, app)
			ext := filepath.Ext(steps[0].name)
			a := e.seed("a" + ext)
			e.seed("c" + ext)

			mnt := t.TempDir()
			if out, err := exec.Command("sshfs", append(append([]string{"-p", e.port}, e.sshOpts()...), "localhost:/", mnt)...).CombinedOutput(); err != nil {
				t.Skipf("sshfs mount failed: %v\n%s", err, out)
			}
			t.Cleanup(func() { _ = exec.Command("fusermount3", "-u", "-z", mnt).Run() })
			assertListed(t, e.run("ls", "-1a", mnt), "a"+ext, "c"+ext)

			home := t.TempDir()
			for _, step := range steps {
				cmd := exec.Command("bash", "-c", step.cmd)
				cmd.Dir = mnt
				cmd.Env = append(os.Environ(), "HOME="+home)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v\n%s", step.cmd, err, out)
				}
				e.assertMoved(step.name, step.text, a, !step.public)
			}
			assertListed(t, e.run("ls", "-1a", mnt), "c"+ext)
		})
	}
}
