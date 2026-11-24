package postfs_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// scopedEnv is env logging in with a cert, signed by env's key, for
// principals.
func (e *postsEnv) scoped(principals ...string) *postsEnv {
	e.t.Helper()
	data, err := os.ReadFile(e.key)
	if err != nil {
		e.t.Fatal(err)
	}
	ca, err := ssh.ParsePrivateKey(data)
	if err != nil {
		e.t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		e.t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		e.t.Fatal(err)
	}
	cert := &ssh.Certificate{Key: signer.PublicKey(), CertType: ssh.UserCert, KeyId: "ci", ValidPrincipals: principals, ValidBefore: ssh.CertTimeInfinity}
	if err := cert.SignCert(rand.Reader, ca); err != nil {
		e.t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		e.t.Fatal(err)
	}
	scoped := *e
	scoped.key = filepath.Join(e.t.TempDir(), "ci")
	if err := os.WriteFile(scoped.key, pem.EncodeToMemory(block), 0o600); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(scoped.key+"-cert.pub", ssh.MarshalAuthorizedKey(cert), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return &scoped
}

func (e *postsEnv) try(stdin, name string, args ...string) (string, bool) {
	cmd := exec.Command(name, append(append(slices.Clone(e.sshOpts()), "-o", "IdentityAgent=none", "-o", "Port="+e.port), args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func TestScopedPosts(t *testing.T) {
	for _, app := range []string{"prose", "pastes"} {
		t.Run(app, func(t *testing.T) {
			env := newPostsEnv(t, app)
			for _, name := range []string{"a.md", "b.md", "ci-1.md"} {
				env.seed(name)
			}
			write := env.scoped(app+":write:a.md", app+":write:ci-*")
			read := env.scoped(app + ":read:a.md")
			text := seedText(env.space)
			local := filepath.Join(t.TempDir(), "up.md")
			if err := os.WriteFile(local, []byte(text+"new\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			cases := []struct {
				name  string
				env   *postsEnv
				batch string
				ok    bool
			}{
				{"get", read, "get /a.md /dev/null", true},
				{"get another", read, "get /b.md /dev/null", false},
				{"put by a reader", read, "put " + local + " /a.md", false},
				{"put", write, "put " + local + " /a.md", true},
				{"put prefix", write, "put " + local + " /ci-2.md", true},
				{"put another", write, "put " + local + " /b.md", false},
				{"put another in a directory", write, "put " + local + " /a.md/b.md", false},
				{"rm another", write, "rm /b.md", false},
				{"rename over another", write, "rename /a.md /b.md", false},
				{"rename another", write, "rename /b.md /a.md", false},
				{"rename within the prefix", write, "rename /ci-2.md /ci-3.md", true},
				{"rm by a reader", read, "rm /a.md", false},
				{"rm", write, "rm /ci-3.md", true},
			}
			for _, c := range cases {
				if out, ok := c.env.try(c.batch+"\n", "sftp", "-b", "-", "127.0.0.1"); ok != c.ok {
					t.Errorf("%s: ok = %v, want %v\n%s", c.name, ok, c.ok, out)
				}
			}

			if out, ok := write.try("new\n", "ssh", "127.0.0.1", "ci-9.md"); !ok {
				t.Errorf("pipe: %s", out)
			}
			if out, ok := write.try("new\n", "ssh", "127.0.0.1", "b.md"); ok {
				t.Errorf("piped over another post: %s", out)
			}
			if out, ok := write.try("new\n", "ssh", "127.0.0.1"); ok {
				t.Errorf("piped a post with a random name: %s", out)
			}

			out, _ := read.try("ls -1 /\n", "sftp", "-b", "-", "127.0.0.1")
			if !strings.Contains(out, "a.md") || strings.Contains(out, "b.md") || strings.Contains(out, "ci-1.md") {
				t.Errorf("listed %q, want only a.md", out)
			}
			if post, err := env.db.FindPostWithFilename("b.md", "user-1", env.space); err != nil || post.Text != text {
				t.Errorf("b.md = %v, %v, want it unchanged", post, err)
			}
			if out, ok := env.scoped(app+":deploy:a.md").try("", "ssh", "127.0.0.1", "command", "ls"); ok || !strings.Contains(out, "Permission denied") {
				t.Errorf("logged in with an unknown role: %s", out)
			}
		})
	}
}
