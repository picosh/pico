package rsync

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"testing"
	"time"

	rsyncutils "github.com/picosh/go-rsync-receiver/utils"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
	"golang.org/x/crypto/ssh"
)

// mockFileInfo implements fs.FileInfo for testing.
type mockFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

func (m *mockFileInfo) Name() string       { return m.name }
func (m *mockFileInfo) Size() int64        { return m.size }
func (m *mockFileInfo) Mode() fs.FileMode  { return m.mode }
func (m *mockFileInfo) ModTime() time.Time { return m.modTime }
func (m *mockFileInfo) IsDir() bool        { return m.isDir }
func (m *mockFileInfo) Sys() any           { return nil }

// mockWriteHandler implements utils.CopyFromClientHandler for testing.
// It records the order of Delete calls to verify deletion order.
type mockWriteHandler struct {
	entries      []os.FileInfo
	deleteCalls  []string
	deleteErrors map[string]error
}

func (m *mockWriteHandler) Delete(_ *pssh.SSHServerConnSession, entry *utils.FileEntry) error {
	m.deleteCalls = append(m.deleteCalls, entry.Filepath)
	if m.deleteErrors != nil {
		if err, ok := m.deleteErrors[entry.Filepath]; ok {
			return err
		}
	}
	return nil
}

func (m *mockWriteHandler) Write(_ *pssh.SSHServerConnSession, _ *utils.FileEntry) (string, error) {
	return "", nil
}

func (m *mockWriteHandler) Read(_ *pssh.SSHServerConnSession, _ *utils.FileEntry) (os.FileInfo, utils.ReadAndReaderAtCloser, error) {
	return nil, nil, nil
}

func (m *mockWriteHandler) List(_ *pssh.SSHServerConnSession, _ string, _ bool, _ bool) ([]os.FileInfo, error) {
	return m.entries, nil
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
func newMockSession() (*pssh.SSHServerConnSession, *bytes.Buffer) {
	stderr := &bytes.Buffer{}
	channel := &mockChannel{stderr: stderr}

	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.Default()
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{})
	serverConn := pssh.NewSSHServerConn(ctx, logger, &ssh.ServerConn{
		Permissions: &ssh.Permissions{
			Extensions: map[string]string{},
		},
	}, server)

	session := &pssh.SSHServerConnSession{
		Channel:       channel,
		SSHServerConn: serverConn,
		Ctx:           ctx,
		CancelFunc:    cancel,
	}

	return session, stderr
}

func TestRemove_DeletesChildrenBeforeParents(t *testing.T) {
	session, _ := newMockSession()
	mockHandler := &mockWriteHandler{
		entries: []os.FileInfo{
			&mockFileInfo{name: "a", isDir: true},
			&mockFileInfo{name: "a/file.txt", size: 100},
			&mockFileInfo{name: "b/c", isDir: true},
			&mockFileInfo{name: "b/c/deep.txt", size: 50},
			&mockFileInfo{name: "b", isDir: true},
		},
	}

	h := &handler{
		session:      session,
		writeHandler: mockHandler,
		root:         "test",
	}

	err := h.Remove([]*rsyncutils.ReceiverFile{})
	if err != nil {
		t.Fatalf("Remove() returned error: %v", err)
	}

	if len(mockHandler.deleteCalls) != 5 {
		t.Fatalf("expected 5 delete calls, got %d: %v", len(mockHandler.deleteCalls), mockHandler.deleteCalls)
	}

	indexOfA := -1
	indexOfAFile := -1
	indexOfB := -1
	indexOfBC := -1
	indexOfBCDeep := -1

	for i, path := range mockHandler.deleteCalls {
		switch path {
		case "/test/a":
			indexOfA = i
		case "/test/a/file.txt":
			indexOfAFile = i
		case "/test/b":
			indexOfB = i
		case "/test/b/c":
			indexOfBC = i
		case "/test/b/c/deep.txt":
			indexOfBCDeep = i
		}
	}

	if indexOfAFile > indexOfA {
		t.Errorf("a/file.txt (index %d) should be deleted before a (index %d)", indexOfAFile, indexOfA)
	}
	if indexOfBCDeep > indexOfBC {
		t.Errorf("b/c/deep.txt (index %d) should be deleted before b/c (index %d)", indexOfBCDeep, indexOfBC)
	}
	if indexOfBC > indexOfB {
		t.Errorf("b/c (index %d) should be deleted before b (index %d)", indexOfBC, indexOfB)
	}
}

func TestRemove_IgnoresPicoKeepDir(t *testing.T) {
	session, _ := newMockSession()
	mockHandler := &mockWriteHandler{
		entries: []os.FileInfo{
			&mockFileInfo{name: "dir", isDir: true},
			&mockFileInfo{name: "dir/._pico_keep_dir", size: 0},
			&mockFileInfo{name: "dir/file.txt", size: 100},
		},
	}

	h := &handler{
		session:      session,
		writeHandler: mockHandler,
		root:         "test",
	}

	err := h.Remove([]*rsyncutils.ReceiverFile{})
	if err != nil {
		t.Fatalf("Remove() returned error: %v", err)
	}

	for _, path := range mockHandler.deleteCalls {
		if path == "/test/dir/._pico_keep_dir" {
			t.Error("._pico_keep_dir should not be in delete list")
		}
	}

	if len(mockHandler.deleteCalls) != 2 {
		t.Errorf("expected 2 delete calls (dir, dir/file.txt), got %d: %v", len(mockHandler.deleteCalls), mockHandler.deleteCalls)
	}
}

func TestRemove_OnlyDeletesFilesNotInWillReceive(t *testing.T) {
	session, _ := newMockSession()
	mockHandler := &mockWriteHandler{
		entries: []os.FileInfo{
			&mockFileInfo{name: "a.txt", size: 100},
			&mockFileInfo{name: "b.txt", size: 100},
			&mockFileInfo{name: "c.txt", size: 100},
		},
	}

	h := &handler{
		session:      session,
		writeHandler: mockHandler,
		root:         "test",
	}

	willReceive := []*rsyncutils.ReceiverFile{
		{Name: "a.txt"},
		{Name: "c.txt"},
	}

	err := h.Remove(willReceive)
	if err != nil {
		t.Fatalf("Remove() returned error: %v", err)
	}

	if len(mockHandler.deleteCalls) != 1 {
		t.Fatalf("expected 1 delete call, got %d: %v", len(mockHandler.deleteCalls), mockHandler.deleteCalls)
	}

	if mockHandler.deleteCalls[0] != "/test/b.txt" {
		t.Errorf("expected to delete /test/b.txt, got %s", mockHandler.deleteCalls[0])
	}
}
