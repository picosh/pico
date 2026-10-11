package prose

import (
	"errors"
	"os"
	"testing"

	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
)

// listHandler lists names and records the renames it is sent.
type listHandler struct {
	ReadWriteHandler
	names   []string
	renamed int
}

func (h *listHandler) List(*pssh.SSHServerConnSession, string, bool, bool) ([]os.FileInfo, error) {
	files := []os.FileInfo{&sendutils.VirtualFile{FName: "/", FIsDir: true}}
	for _, name := range h.names {
		files = append(files, &sendutils.VirtualFile{FName: name})
	}
	return files, nil
}

func (h *listHandler) Rename(*pssh.SSHServerConnSession, *sendutils.FileEntry, *sendutils.FileEntry) error {
	h.renamed++
	return nil
}

func entry(name string) *sendutils.FileEntry {
	return &sendutils.FileEntry{Filepath: "/" + name}
}

func TestRouterSharedHandler(t *testing.T) {
	posts := &listHandler{names: []string{"a.md"}}
	router := newFileRouter("prose", map[string]ReadWriteHandler{
		".md":      posts,
		".lxt":     posts,
		".txt":     &listHandler{names: []string{"a.md"}},
		"fallback": &listHandler{names: []string{"x.png"}},
	})

	list, err := router.List(nil, "/", true, false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range list {
		names = append(names, f.Name())
	}
	if len(names) != 3 {
		t.Errorf("listed %q, want /, a.md and x.png once each", names)
	}

	if err := router.Rename(nil, entry("a.md"), entry("a.lxt")); err != nil || posts.renamed != 1 {
		t.Fatalf("rename within a handler: %v, renamed %d", err, posts.renamed)
	}
	for _, to := range []string{"a.txt", "a.png"} {
		if err := router.Rename(nil, entry("a.lxt"), entry(to)); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("rename to %s across handlers: %v", to, err)
		}
	}
}
