package scoped

import (
	"errors"
	"log/slog"
	"os"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
	"golang.org/x/crypto/ssh"
)

// recorder accepts everything and records what reached it.
type recorder struct {
	calls []string
}

func (r *recorder) call(args ...string) { r.calls = append(r.calls, strings.Join(args, " ")) }

func (r *recorder) Delete(_ *pssh.SSHServerConnSession, e *utils.FileEntry) error {
	r.call("delete", e.Filepath)
	return nil
}

func (r *recorder) Write(_ *pssh.SSHServerConnSession, e *utils.FileEntry) (string, error) {
	r.call("write", e.Filepath)
	return "", nil
}

func (r *recorder) ValidateWrite(_ *pssh.SSHServerConnSession, e *utils.FileEntry) error {
	r.call("validate", e.Filepath)
	return nil
}

func (r *recorder) Rename(_ *pssh.SSHServerConnSession, from, to *utils.FileEntry) error {
	r.call("rename", from.Filepath, to.Filepath)
	return nil
}

func (r *recorder) Read(_ *pssh.SSHServerConnSession, e *utils.FileEntry) (os.FileInfo, utils.ReadAndReaderAtCloser, error) {
	r.call("read", e.Filepath)
	return nil, nil, nil
}

func (r *recorder) List(_ *pssh.SSHServerConnSession, fpath string, _, _ bool) ([]os.FileInfo, error) {
	r.call("list", fpath)
	var out []os.FileInfo
	for _, name := range []string{"", "blog/", "blog/index.html", "shop", "shop/index.html", "blog2"} {
		out = append(out, &utils.VirtualFile{FName: name})
	}
	return out, nil
}

func (r *recorder) IsImplicitDir(*pssh.SSHServerConnSession, string) bool { return true }
func (r *recorder) ProtectRoot() bool                                     { return true }
func (r *recorder) GetLogger(*pssh.SSHServerConnSession) *slog.Logger     { return slog.Default() }
func (r *recorder) Validate(*pssh.SSHServerConnSession) error             { return nil }

func session(scopes string) *pssh.SSHServerConnSession {
	extensions := map[string]string{}
	if scopes != "" {
		extensions["scopes"] = scopes
	}
	return &pssh.SSHServerConnSession{SSHServerConn: &pssh.SSHServerConn{
		Conn: &ssh.ServerConn{Permissions: &ssh.Permissions{Extensions: extensions}},
	}}
}

func TestHandler(t *testing.T) {
	entry := func(p string) *utils.FileEntry { return &utils.FileEntry{Filepath: p} }
	ops := map[string]func(*Handler, *pssh.SSHServerConnSession, string) error{
		"delete":   func(h *Handler, s *pssh.SSHServerConnSession, p string) error { return h.Delete(s, entry(p)) },
		"validate": func(h *Handler, s *pssh.SSHServerConnSession, p string) error { return h.ValidateWrite(s, entry(p)) },
		"write": func(h *Handler, s *pssh.SSHServerConnSession, p string) error {
			_, err := h.Write(s, entry(p))
			return err
		},
		"read": func(h *Handler, s *pssh.SSHServerConnSession, p string) error {
			_, _, err := h.Read(s, entry(p))
			return err
		},
		"list": func(h *Handler, s *pssh.SSHServerConnSession, p string) error {
			_, err := h.List(s, p, true, false)
			return err
		},
		"rename from": func(h *Handler, s *pssh.SSHServerConnSession, p string) error {
			return h.Rename(s, entry(p), entry("/blog/x"))
		},
		"rename to": func(h *Handler, s *pssh.SSHServerConnSession, p string) error {
			return h.Rename(s, entry("/blog/x"), entry(p))
		},
	}

	rules := Rules{Read: []string{"read", "deploy"}, Write: []string{"deploy"}, Resource: Project}
	const deploy, read = `[{"role":"deploy","resource":"blog"}]`, `[{"role":"read","resource":"blog"}]`
	reads := []string{"read", "list"}

	for op, run := range ops {
		for _, c := range []struct {
			scopes, path string
			ok           bool
		}{
			{"", "/shop/x", true},
			{"", "/blog/../shop/x", true},
			{deploy, "/blog/x", true},
			{deploy, "blog/x", true},
			{deploy, "//blog//x", true},
			{deploy, "/./blog/x", true},
			{deploy, "/shop/x", false},
			{deploy, "/blog2/x", false},
			{deploy, "/blog/../shop/x", false},
			{deploy, "/shop/../blog/x", false},
			{deploy, "..", false},
			{deploy, "/", op == "list"},
			{read, "/blog/x", slices.Contains(reads, op)},
			{read, "/shop/x", false},
			{"nonsense", "/blog/x", false},
		} {
			inner := &recorder{}
			err := run(NewHandler(inner, rules), session(c.scopes), c.path)
			if (err == nil) != c.ok || (len(inner.calls) > 0) != c.ok {
				t.Errorf("%s %s with %q: %v, reached %q, want ok = %v", op, c.path, c.scopes, err, inner.calls, c.ok)
			}
		}
	}
}

func TestHandlerRoot(t *testing.T) {
	names := func(h *Handler, scopes string) []string {
		entries, err := h.List(session(scopes), "/", true, true)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}

	h := NewHandler(&recorder{}, Rules{Read: []string{"read"}, Resource: Project})
	if got, want := names(h, `[{"role":"read","resource":"blog"}]`), []string{"", "blog/", "blog/index.html"}; !slices.Equal(got, want) {
		t.Errorf("root = %q, want %q", got, want)
	}
	if got := names(h, ""); len(got) != 6 {
		t.Errorf("unlimited root = %q", got)
	}
	if !h.ProtectRoot() || !h.IsImplicitDir(session(""), "/shop") || h.IsImplicitDir(session(`[{"role":"read","resource":"blog"}]`), "/shop") {
		t.Error("optional interfaces don't follow the inner handler and the scopes")
	}

	files := NewHandler(&recorder{}, Rules{Read: []string{"read"}, Resource: path.Base})
	if got, want := names(files, `[{"role":"read","resource":"index.html"}]`), []string{"", "blog/index.html", "shop/index.html"}; !slices.Equal(got, want) {
		t.Errorf("files = %q, want %q", got, want)
	}
}

type plain struct{ utils.CopyFromClientHandler }

func TestHandlerWithoutOptionalInterfaces(t *testing.T) {
	h := NewHandler(plain{&recorder{}}, Rules{Resource: Project})
	s := session("")
	if err := h.Rename(s, &utils.FileEntry{}, &utils.FileEntry{}); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Rename() = %v, want unsupported", err)
	}
	if h.ValidateWrite(s, &utils.FileEntry{}) != nil || h.ProtectRoot() || h.IsImplicitDir(s, "/x") {
		t.Error("a handler without optional interfaces has them")
	}
}
