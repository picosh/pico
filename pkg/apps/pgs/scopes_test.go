package pgs

import (
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

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
	"golang.org/x/crypto/ssh"
)

// scopedArgs are ssh options that log in with a cert, signed by the user's
// key, for principals.
func (e *rsyncEnv) scopedArgs(principals ...string) []string {
	e.t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		e.t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		e.t.Fatal(err)
	}
	cert := &ssh.Certificate{
		Key:             signer.PublicKey(),
		CertType:        ssh.UserCert,
		KeyId:           "ci",
		ValidPrincipals: principals,
		ValidBefore:     ssh.CertTimeInfinity,
	}
	if err := cert.SignCert(rand.Reader, e.user.signer); err != nil {
		e.t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		e.t.Fatal(err)
	}

	key := filepath.Join(e.t.TempDir(), "ci")
	if err := os.WriteFile(key, pem.EncodeToMemory(block), 0o600); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(key+"-cert.pub", ssh.MarshalAuthorizedKey(cert), 0o600); err != nil {
		e.t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(e.addr)
	return []string{"-F", "/dev/null", "-o", "Port=" + port, "-i", key, "-o", "CertificateFile=" + key + "-cert.pub", "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR"}
}

func (e *rsyncEnv) scopedSSH(args []string, stdin string, command ...string) (string, int) {
	e.t.Helper()
	cmd := exec.Command("ssh", append(append(slices.Clone(args), "127.0.0.1"), command...)...)
	cmd.Stdin = strings.NewReader(stdin)
	return runCmd(e.t, cmd)
}

func newScopedEnv(t *testing.T) *rsyncEnv {
	t.Helper()
	st, err := storage.NewStorageMemory(map[string]map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	env := newRsyncEnv(t, st)
	full := env.scopedArgs("pgs")
	for _, name := range []string{"blog", "blog-v1", "shop", "private-notes"} {
		if out, code := env.scopedSSH(full, name, "/"+name+"/index.html"); code != 0 {
			t.Fatalf("upload %s: %d %s", name, code, out)
		}
	}
	return env
}

func TestScopedCommands(t *testing.T) {
	env := newScopedEnv(t)
	deploy := env.scopedArgs("pgs:deploy:blog")
	prefix := env.scopedArgs("pgs:deploy:blog*")
	read := env.scopedArgs("pgs:read:blog")
	readAll := env.scopedArgs("pgs:read")
	link := env.scopedArgs("pgs:deploy:blog", "pgs:read:blog-v1")

	cases := []struct {
		name    string
		args    []string
		command []string
		denied  bool
		want    string
		without string
	}{
		{name: "help", args: read, command: []string{"help"}, want: "pgs.sh"},
		{name: "ls", args: deploy, command: []string{"ls"}, want: "blog", without: "shop"},
		{name: "ls prefix", args: prefix, command: []string{"ls"}, want: "blog-v1", without: "shop"},
		{name: "command ls", args: deploy, command: []string{"command", "ls"}, want: "blog/", without: "shop"},
		{name: "stats", args: deploy, command: []string{"stats"}, denied: true},
		{name: "stats of everything", args: readAll, command: []string{"stats"}, want: "Quota"},
		{name: "fzf", args: read, command: []string{"fzf", "blog"}, want: "index.html"},
		{name: "fzf another", args: deploy, command: []string{"fzf", "shop"}, denied: true},
		{name: "link", args: link, command: []string{"link", "blog", "--to", "blog-v1"}, want: "now points to"},
		{name: "link to another", args: deploy, command: []string{"link", "blog", "--to", "private-notes"}, denied: true},
		{name: "link another", args: deploy, command: []string{"link", "shop", "--to", "blog"}, denied: true},
		{name: "link by a reader", args: read, command: []string{"link", "blog", "--to", "blog"}, denied: true},
		{name: "unlink", args: deploy, command: []string{"unlink", "blog"}, want: "unlinked"},
		{name: "unlink another", args: deploy, command: []string{"unlink", "shop"}, denied: true},
		{name: "depends another", args: deploy, command: []string{"depends", "shop"}, denied: true},
		{name: "rm", args: deploy, command: []string{"rm", "blog"}, want: "(blog) removing"},
		{name: "rm another", args: deploy, command: []string{"rm", "shop", "--write"}, denied: true},
		{name: "rm by a reader", args: read, command: []string{"rm", "blog", "--write"}, denied: true},
		{name: "cache another", args: deploy, command: []string{"cache", "shop"}, denied: true},
		{name: "cache-all", args: env.scopedArgs("pgs:deploy"), command: []string{"cache-all"}, denied: true},
		{name: "acl", args: env.scopedArgs("pgs:deploy"), command: []string{"acl", "blog", "--type", "public"}, denied: true},
		{name: "forms", args: deploy, command: []string{"forms", "ls"}, denied: true},
		{name: "forms of everything", args: readAll, command: []string{"forms", "ls"}, want: "no forms"},
		{name: "forms rm by a reader", args: readAll, command: []string{"forms", "x", "--rm"}, denied: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, code := env.scopedSSH(c.args, "", c.command...)
			if c.denied {
				if code == 0 || !strings.Contains(out, "this certificate doesn't allow") {
					t.Errorf("exit %d, %q, want denied", code, out)
				}
				return
			}
			if !strings.Contains(out, c.want) || strings.Contains(out, "certificate doesn't allow") {
				t.Errorf("exit %d, %q, want %q", code, out, c.want)
			}
			if c.without != "" && strings.Contains(out, c.without) {
				t.Errorf("%q shows %q", out, c.without)
			}
		})
	}
	for _, name := range []string{"shop", "blog"} {
		if data, _, _ := env.stored("/" + name + "/index.html"); string(data) != name {
			t.Errorf("%s = %q", name, data)
		}
	}
}

// prefixDB finds projects by prefix, which the memory database doesn't.
type prefixDB struct{ pgsdb.PgsDB }

func (d prefixDB) FindProjectsByPrefix(userID, prefix string) ([]*db.Project, error) {
	projects, err := d.FindProjectsByUser(userID)
	return slices.DeleteFunc(slices.Clone(projects), func(p *db.Project) bool { return !strings.HasPrefix(p.Name, prefix) }), err
}

func (d prefixDB) FindProjectLinks(string, string) ([]*db.Project, error) { return nil, nil }

func TestScopedPrune(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	dbpool := NewPgsDb(logger)
	user := dbpool.Users[0]
	files := map[string]string{}
	for _, name := range []string{"blog-v1", "blog-v2", "blogroll", "shop"} {
		if _, err := dbpool.InsertProject(user.ID, name, name); err != nil {
			t.Fatal(err)
		}
		files["/"+name+"/index.html"] = name
	}
	st, err := storage.NewStorageMemory(map[string]map[string]string{shared.GetAssetBucketName(user.ID): files})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		scopes shared.Scopes
		prefix string
		want   []string
	}{
		{nil, "blog", []string{"blog-v1", "blog-v2", "blogroll"}},
		{shared.Scopes{{Role: roleDeploy, Resource: "blog-*"}}, "blog", []string{"blog-v1", "blog-v2"}},
		{shared.Scopes{{Role: roleDeploy, Resource: "blog-v1"}}, "blog", []string{"blog-v1"}},
		{shared.Scopes{{Role: roleDeploy, Resource: "blog-*"}}, "shop", nil},
		{shared.Scopes{{Role: roleRead}}, "blog", nil},
	}
	for _, c := range cases {
		out := &fakeCmdSession{}
		cmd := &Cmd{User: user, Session: out, Log: logger, Store: st, Dbpool: prefixDB{dbpool}, Cfg: NewConfig(logger, dbpool, st, discardPubsub{}), Scopes: c.scopes}
		if err := cmd.prune(c.prefix, 0); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"blog-v1", "blog-v2", "blogroll", "shop"} {
			if got := strings.Contains(out.String(), "project ("+name+") removed"); got != slices.Contains(c.want, name) {
				t.Errorf("prune %s with %v removes %s: %v", c.prefix, c.scopes, name, got)
			}
		}
	}
}

func TestScopedFiles(t *testing.T) {
	env := newScopedEnv(t)
	deploy := env.scopedArgs("pgs:deploy:blog", "pgs:deploy:new")
	read := env.scopedArgs("pgs:read:blog")
	local := t.TempDir()
	if err := os.WriteFile(filepath.Join(local, "a.html"), []byte("ci"), 0o600); err != nil {
		t.Fatal(err)
	}
	rsync := func(args []string, rest ...string) (string, int) {
		return runCmd(t, exec.Command("rsync", append([]string{"-e", "ssh " + strings.Join(args, " "), "-r"}, rest...)...))
	}
	sftp := func(args []string, batch string) (string, int) {
		cmd := exec.Command("sftp", append(append([]string{"-b", "-"}, args...), "127.0.0.1")...)
		cmd.Stdin = strings.NewReader(batch)
		return runCmd(t, cmd)
	}
	scp := func(args []string, to string) (string, int) {
		return runCmd(t, exec.Command("scp", append(append([]string{"-O"}, args...), filepath.Join(local, "a.html"), "127.0.0.1:"+to)...))
	}
	file := filepath.Join(local, "a.html")

	cases := []struct {
		name string
		run  func() (string, int)
		ok   bool
	}{
		{"pipe", func() (string, int) { return env.scopedSSH(deploy, "ci", "/blog/pipe.html") }, true},
		{"pipe new project", func() (string, int) { return env.scopedSSH(deploy, "ci", "/new/pipe.html") }, true},
		{"pipe another", func() (string, int) { return env.scopedSSH(deploy, "ci", "/shop/pipe.html") }, false},
		{"pipe by a reader", func() (string, int) { return env.scopedSSH(read, "ci", "/blog/r.html") }, false},
		{"pipe dots", func() (string, int) { return env.scopedSSH(deploy, "ci", "/blog/../shop/pipe.html") }, false},
		{"rsync", func() (string, int) { return rsync(deploy, local+"/", "127.0.0.1:/blog/rs/") }, true},
		{"rsync another", func() (string, int) { return rsync(deploy, local+"/", "127.0.0.1:/shop/rs/") }, false},
		{"rsync delete another", func() (string, int) { return rsync(deploy, "--delete", local+"/", "127.0.0.1:/shop/") }, false},
		{"rsync by a reader", func() (string, int) { return rsync(read, local+"/", "127.0.0.1:/blog/rr/") }, false},
		{"rsync fetch", func() (string, int) { return rsync(read, "127.0.0.1:/blog/", t.TempDir()) }, true},
		{"rsync fetch another", func() (string, int) { return rsync(read, "127.0.0.1:/shop/", t.TempDir()) }, false},
		{"rsync fetch private", func() (string, int) { return rsync(read, "127.0.0.1:/private-notes/", t.TempDir()) }, false},
		{"scp", func() (string, int) { return scp(deploy, "/blog/") }, true},
		{"scp another", func() (string, int) { return scp(deploy, "/shop/") }, false},
		{"sftp put", func() (string, int) { return sftp(deploy, "put "+file+" /blog/s.html\n") }, true},
		{"sftp put another", func() (string, int) { return sftp(deploy, "put "+file+" /shop/s.html\n") }, false},
		{"sftp get another", func() (string, int) { return sftp(read, "get /shop/index.html /dev/null\n") }, false},
		{"sftp ls another", func() (string, int) { return sftp(read, "ls /shop/\n") }, false},
		{"sftp rm another", func() (string, int) { return sftp(deploy, "rm /shop/index.html\n") }, false},
		{"sftp rename out", func() (string, int) { return sftp(deploy, "rename /blog/index.html /shop/stolen.html\n") }, false},
		{"sftp rename in", func() (string, int) { return sftp(deploy, "rename /shop/index.html /blog/stolen.html\n") }, false},
		{"sftp mkdir another", func() (string, int) { return sftp(deploy, "mkdir /shop/dir\n") }, false},
		{"sftp rm by a reader", func() (string, int) { return sftp(read, "rm /blog/index.html\n") }, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if out, code := c.run(); (code == 0) != c.ok {
				t.Errorf("exit %d, want ok = %v\n%s", code, c.ok, out)
			}
		})
	}

	// The whole account, fetched, is only what the scopes cover.
	dir := t.TempDir()
	if out, code := rsync(read, "127.0.0.1:/", dir); code != 0 {
		t.Errorf("fetch root: %d\n%s", code, out)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "blog" {
		t.Errorf("fetched %v, want only blog", entries)
	}
	out, _ := sftp(read, "ls -1 /\n")
	if !strings.Contains(out, "blog") || strings.Contains(out, "shop") || strings.Contains(out, "blog-v1") {
		t.Errorf("sftp root = %q", out)
	}

	for name, want := range map[string]string{
		"/blog/index.html": "blog", "/blog/pipe.html": "ci", "/blog/rs/a.html": "ci", "/blog/a.html": "ci", "/blog/s.html": "ci",
		"/new/pipe.html": "ci", "/shop/index.html": "shop", "/private-notes/index.html": "private-notes",
	} {
		if data, _, _ := env.stored(name); string(data) != want {
			t.Errorf("%s = %q, want %q", name, data, want)
		}
	}
	for _, name := range []string{"/shop/pipe.html", "/shop/rs/a.html", "/shop/a.html", "/shop/s.html", "/shop/stolen.html", "/blog/stolen.html", "/blog/r.html", "/blog/rr/a.html"} {
		if _, _, ok := env.stored(name); ok {
			t.Errorf("%s was stored", name)
		}
	}
}

func TestScopedLogin(t *testing.T) {
	env := newScopedEnv(t)
	for name, args := range map[string][]string{
		"unknown role":   env.scopedArgs("pgs:admin:blog"),
		"one bad scope":  env.scopedArgs("pgs:deploy:blog", "pgs:write:shop"),
		"other service":  env.scopedArgs("picd:deploy:blog"),
		"admin session":  append(env.scopedArgs("pgs:deploy:blog"), "-l", "admin__user"),
		"empty resource": env.scopedArgs("pgs:deploy:"),
	} {
		if out, code := env.scopedSSH(args, "", "help"); code != 255 || !strings.Contains(out, "Permission denied") {
			t.Errorf("%s: exit %d, %q, want refused", name, code, out)
		}
	}
}

func TestTunnelScopes(t *testing.T) {
	owner, other := &db.User{ID: "1"}, &db.User{ID: "2"}
	blog := &db.Project{Name: "blog"}
	scoped := shared.Scopes{{Role: "read", Resource: "blog"}}

	cases := []struct {
		name      string
		scopes    shared.Scopes
		asUser    string
		requester *db.User
		project   *db.Project
		ok        bool
	}{
		{"unlimited", nil, "victim", other, blog, true},
		{"own", scoped, "", owner, blog, true},
		{"own, another project", scoped, "", owner, &db.Project{Name: "private-notes"}, false},
		{"someone else's of the same name", scoped, "", other, blog, false},
		{"everything", shared.Scopes{{Role: "read"}}, "", other, blog, true},
		{"impersonation", shared.Scopes{{Role: "deploy"}}, "owner", owner, blog, false},
		{"nothing", shared.Scopes{}, "", owner, blog, false},
	}
	for _, c := range cases {
		if err := checkTunnelScopes(c.scopes, c.asUser, owner, c.requester, c.project); (err == nil) != c.ok {
			t.Errorf("%s: %v, want ok = %v", c.name, err, c.ok)
		}
	}
}
