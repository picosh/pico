package rsync

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/rsync"
	"github.com/picosh/pico/pkg/send/utils"
	"golang.org/x/crypto/ssh"
)

// mockFileInfo implements fs.FileInfo for testing.
type mockFileInfo struct {
	name    string
	size    int64
	modTime time.Time
	isDir   bool
}

func (m *mockFileInfo) Name() string { return m.name }
func (m *mockFileInfo) Size() int64  { return m.size }
func (m *mockFileInfo) Mode() fs.FileMode {
	if m.isDir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (m *mockFileInfo) ModTime() time.Time { return m.modTime }
func (m *mockFileInfo) IsDir() bool        { return m.isDir }
func (m *mockFileInfo) Sys() any           { return nil }

// mockWriteHandler implements utils.CopyFromClientHandler and records the
// paths it was called with.
type mockWriteHandler struct {
	lists   map[string][]os.FileInfo
	files   map[string]string
	calls   []string
	written map[string]string
}

func (m *mockWriteHandler) Delete(_ *pssh.SSHServerConnSession, entry *utils.FileEntry) error {
	m.calls = append(m.calls, "delete "+entry.Filepath)
	return nil
}

func (m *mockWriteHandler) Write(_ *pssh.SSHServerConnSession, entry *utils.FileEntry) (string, error) {
	m.calls = append(m.calls, "write "+entry.Filepath)
	b, err := io.ReadAll(entry.Reader)
	if err != nil {
		return "", err
	}
	if m.written == nil {
		m.written = map[string]string{}
	}
	m.written[entry.Filepath] = string(b)
	return "", nil
}

func (m *mockWriteHandler) Read(_ *pssh.SSHServerConnSession, entry *utils.FileEntry) (os.FileInfo, utils.ReadAndReaderAtCloser, error) {
	m.calls = append(m.calls, "read "+entry.Filepath)
	data, ok := m.files[entry.Filepath]
	if !ok {
		return nil, nil, os.ErrNotExist
	}
	info := &mockFileInfo{name: entry.Filepath, size: int64(len(data))}
	return info, utils.NopReadAndReaderAtCloser(strings.NewReader(data)), nil
}

func (m *mockWriteHandler) List(_ *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	m.calls = append(m.calls, "list "+fpath)
	return m.lists[fpath], nil
}

func (m *mockWriteHandler) GetLogger(_ *pssh.SSHServerConnSession) *slog.Logger {
	return slog.Default()
}

func (m *mockWriteHandler) Validate(_ *pssh.SSHServerConnSession) error {
	return nil
}

// mockChannel implements ssh.Channel for testing.
type mockChannel struct {
	stderr *bytes.Buffer
}

func (m *mockChannel) Read(_ []byte) (int, error)     { return 0, io.EOF }
func (m *mockChannel) Write(data []byte) (int, error) { return len(data), nil }
func (m *mockChannel) Close() error                   { return nil }
func (m *mockChannel) CloseWrite() error              { return nil }
func (m *mockChannel) SendRequest(_ string, _ bool, _ []byte) (bool, error) {
	return true, nil
}
func (m *mockChannel) Stderr() io.ReadWriter { return m.stderr }

var _ ssh.Channel = (*mockChannel)(nil)

// newMockSession creates a mock SSHServerConnSession for testing.
func newMockSession() *pssh.SSHServerConnSession {
	channel := &mockChannel{stderr: &bytes.Buffer{}}

	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.Default()
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{})
	serverConn := pssh.NewSSHServerConn(ctx, logger, &ssh.ServerConn{
		Permissions: &ssh.Permissions{
			Extensions: map[string]string{},
		},
	}, server)

	return &pssh.SSHServerConnSession{
		Channel:       channel,
		SSHServerConn: serverConn,
		Ctx:           ctx,
		CancelFunc:    cancel,
	}
}

func TestReadDirNormalizesListing(t *testing.T) {
	handler := &mockWriteHandler{lists: map[string][]os.FileInfo{
		"/site": {
			&mockFileInfo{name: "", isDir: true},
			&mockFileInfo{name: "index.html", size: 10},
			&mockFileInfo{name: "/css/site.css", size: 5},
			&mockFileInfo{name: "css", isDir: true},
			&mockFileInfo{name: "empty.txt"},
		},
	}}
	fsys := &storageFS{session: newMockSession(), handler: handler}

	entries, err := fsys.ReadDir("site", true)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	want := []string{"index.html", "css/site.css", "css", "empty.txt"}
	if !slices.Equal(names, want) {
		t.Errorf("got %q, want %q", names, want)
	}
}

func TestStat(t *testing.T) {
	handler := &mockWriteHandler{lists: map[string][]os.FileInfo{
		"/site":            {&mockFileInfo{name: "", isDir: true}},
		"/site/index.html": {&mockFileInfo{name: "index.html", size: 42}},
		"/":                {&mockFileInfo{name: "/", isDir: true}, &mockFileInfo{name: "hello.md"}},
		"/posts":           {&mockFileInfo{name: "a.md"}, &mockFileInfo{name: "b.md"}},
	}}
	fsys := &storageFS{session: newMockSession(), handler: handler}

	if info, err := fsys.Stat("site"); err != nil || !info.IsDir {
		t.Errorf("site: %+v, %v", info, err)
	}
	if info, err := fsys.Stat("site/index.html"); err != nil || info.IsDir || info.Size != 42 {
		t.Errorf("site/index.html: %+v, %v", info, err)
	}
	if info, err := fsys.Stat(""); err != nil || !info.IsDir {
		t.Errorf("root: %+v, %v", info, err)
	}
	if info, err := fsys.Stat("posts"); err != nil || !info.IsDir {
		t.Errorf("posts: %+v, %v", info, err)
	}
	if _, err := fsys.Stat("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("nope: %v", err)
	}
}

func TestHandlerPathsAreAbsolute(t *testing.T) {
	handler := &mockWriteHandler{files: map[string]string{"/site/a.txt": "hello"}}
	fsys := &storageFS{session: newMockSession(), handler: handler}

	f, info, err := fsys.Open("site/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, info.Size)
	if _, err := f.ReadAt(buf, 0); err != nil || string(buf) != "hello" {
		t.Fatalf("read %q, %v", buf, err)
	}
	if _, err := fsys.Put("site/b.txt", fsInfo(5), strings.NewReader("world")); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Remove("site/c.txt", false); err != nil {
		t.Fatal(err)
	}
	want := []string{"read /site/a.txt", "write /site/b.txt", "delete /site/c.txt"}
	if !slices.Equal(handler.calls, want) {
		t.Errorf("calls %q, want %q", handler.calls, want)
	}
	if handler.written["/site/b.txt"] != "world" {
		t.Errorf("written %q", handler.written)
	}
}

func fsInfo(size int64) rsync.FileInfo {
	return rsync.FileInfo{Size: size, ModTime: time.Unix(1700000000, 0)}
}
