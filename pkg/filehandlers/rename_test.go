package filehandlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
	"golang.org/x/crypto/ssh"
)

// extHooks accepts .md and .lxt files, and tags each with its extension.
type extHooks struct {
	// meta runs while a post's metadata is read, as a save from another
	// session could.
	meta func()
}

func (extHooks) FileValidate(_ *pssh.SSHServerConnSession, data *PostMetaData) (bool, error) {
	if ext := filepath.Ext(data.Filename); ext != ".md" && ext != ".lxt" {
		return false, fmt.Errorf("%s: invalid extension", data.Filename)
	}
	return true, nil
}

func (h extHooks) FileMeta(_ *pssh.SSHServerConnSession, data *PostMetaData) error {
	if h.meta != nil {
		h.meta()
	}
	ext := filepath.Ext(data.Filename)
	data.Tags = []string{ext}
	data.Aliases = []string{data.Slug + ext}
	data.Title = data.Slug
	if data.Cur != nil {
		data.Hidden = data.Cur.Hidden
	}
	return nil
}

func newPostsSession(t *testing.T) (*pssh.SSHServerConnSession, *stub.PostsDB) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	user := &db.User{ID: "user-1", Name: "tester"}
	session := &pssh.SSHServerConnSession{
		SSHServerConn: &pssh.SSHServerConn{
			Conn:   &ssh.ServerConn{Permissions: &ssh.Permissions{Extensions: map[string]string{}}},
			Logger: logger,
		},
		Ctx: context.Background(),
	}
	pssh.SetUser(session, user)
	return session, stub.NewPostsDB(logger, user)
}

func insertPost(t *testing.T, posts *stub.PostsDB, filename, text string, hidden bool) *db.Post {
	t.Helper()
	p := newPost(filename, []byte(text))
	publish := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	p.UserID, p.Space, p.Hidden, p.PublishAt, p.UpdatedAt = "user-1", "prose", hidden, &publish, &publish
	created, err := posts.InsertPost(&p)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func TestPostRename(t *testing.T) {
	session, posts := newPostsSession(t)
	h := NewScpPostHandler(posts, &shared.ConfigSite{Space: "prose"}, extHooks{})
	a := insertPost(t, posts, "a.md", "# a", true)
	b := insertPost(t, posts, "b.md", "# b", false)
	insertPost(t, posts, "c.md", "# c", false)
	rename := func(from, to string) error {
		return h.Rename(session, &sendutils.FileEntry{Filepath: "/" + from}, &sendutils.FileEntry{Filepath: "/" + to})
	}

	if err := rename("a.md", "a.lxt"); err != nil {
		t.Fatal(err)
	}
	got, err := posts.FindPostWithFilename("a.lxt", "user-1", "prose")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != a.ID || !got.PublishAt.Equal(*a.PublishAt) || !got.Hidden || got.Text != "# a" || got.MimeType == a.MimeType {
		t.Errorf("renamed post = %+v, was %+v", got, a)
	}

	// c.lxt has the slug c.md has.
	if err := rename("a.lxt", "c.lxt"); !errors.Is(err, os.ErrExist) {
		t.Errorf("rename onto a used slug: %v", err)
	}
	if err := rename("a.lxt", "a.txt"); err == nil {
		t.Error("rename to an invalid name succeeded")
	}
	if err := rename("missing.md", "m.md"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rename of a missing post: %v", err)
	}

	if err := rename("a.lxt", "b.md"); err != nil {
		t.Fatal(err)
	}
	got, err = posts.FindPostWithFilename("b.md", "user-1", "prose")
	if err != nil || got.ID != a.ID || got.Text != "# a" {
		t.Errorf("b.md = %+v, %v", got, err)
	}
	if _, err := posts.FindPost(b.ID); err == nil {
		t.Error("replaced post still exists")
	}
	all, _ := posts.FindAllPostsByUser("user-1", "prose")
	if len(all) != 2 {
		t.Errorf("posts = %d, want 2", len(all))
	}
}

// otherHandler is a handler of a different kind, like prose's images.
type otherHandler struct{ ReadWriteHandler }

func (otherHandler) List(*pssh.SSHServerConnSession, string, bool, bool) ([]os.FileInfo, error) {
	return []os.FileInfo{&sendutils.VirtualFile{FName: "/", FIsDir: true}, &sendutils.VirtualFile{FName: "x.png"}}, nil
}

func TestRouterSharedHandler(t *testing.T) {
	session, posts := newPostsSession(t)
	cfg := &shared.ConfigSite{Space: "prose"}
	postHandler := NewScpPostHandler(posts, cfg, extHooks{})
	router := NewFileHandlerRouter(cfg, posts, map[string]ReadWriteHandler{
		".md":      postHandler,
		".lxt":     postHandler,
		".txt":     NewScpPostHandler(posts, cfg, extHooks{}),
		"fallback": otherHandler{},
	})
	insertPost(t, posts, "a.md", "# a", false)

	list, err := router.List(session, "/", true, false)
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

	entry := func(name string) *sendutils.FileEntry { return &sendutils.FileEntry{Filepath: "/" + name} }
	if err := router.Rename(session, entry("a.md"), entry("a.lxt")); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"a.txt", "a.png"} {
		if err := router.Rename(session, entry("a.lxt"), entry(to)); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("rename to %s across handlers: %v", to, err)
		}
	}
}

func TestPostRenameRelations(t *testing.T) {
	session, posts := newPostsSession(t)
	h := NewScpPostHandler(posts, &shared.ConfigSite{Space: "prose"}, extHooks{})
	a := insertPost(t, posts, "a.md", "# a", false)
	if err := h.Rename(session, &sendutils.FileEntry{Filepath: "/a.md"}, &sendutils.FileEntry{Filepath: "/a.lxt"}); err != nil {
		t.Fatal(err)
	}
	got, err := posts.FindPost(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Tags, []string{".lxt"}) || !slices.Equal(posts.Aliases(a.ID), []string{"a.lxt"}) {
		t.Errorf("tags %q and aliases %q, want the .lxt ones", got.Tags, posts.Aliases(a.ID))
	}
}

func TestPostRenameConcurrentSave(t *testing.T) {
	for _, tt := range []struct {
		name string
		// saves is how many renames a save interrupts.
		saves int
		// post is the post that is saved.
		post string
		want error
	}{
		{name: "source saved once", saves: 1, post: "a.md"},
		{name: "replaced post saved once", saves: 1, post: "b.md"},
		{name: "source saved on every try", saves: 3, post: "a.md", want: db.ErrPostChanged},
		{name: "replaced post saved on every try", saves: 3, post: "b.md", want: db.ErrPostChanged},
	} {
		t.Run(tt.name, func(t *testing.T) {
			session, posts := newPostsSession(t)
			a := insertPost(t, posts, "a.md", "# a", true)
			b := insertPost(t, posts, "b.md", "# b", false)
			saves := 0
			save := func() {
				if saves == tt.saves {
					return
				}
				saves++
				p, err := posts.FindPostWithFilename(tt.post, "user-1", "prose")
				if err != nil {
					t.Fatal(err)
				}
				p.Text = fmt.Sprintf("save %d", saves)
				now := time.Now()
				p.UpdatedAt = &now
				if _, err := posts.UpdatePost(p); err != nil {
					t.Fatal(err)
				}
			}
			h := NewScpPostHandler(posts, &shared.ConfigSite{Space: "prose"}, extHooks{meta: save})

			err := h.Rename(session, &sendutils.FileEntry{Filepath: "/a.md"}, &sendutils.FileEntry{Filepath: "/b.md"})
			if !errors.Is(err, tt.want) {
				t.Fatalf("rename: %v, want %v", err, tt.want)
			}
			lastSave := fmt.Sprintf("save %d", saves)
			if tt.want != nil {
				// Both posts are where they were, with the last save.
				for _, p := range []*db.Post{a, b} {
					got, err := posts.FindPost(p.ID)
					if err != nil {
						t.Fatalf("%s: %v", p.Filename, err)
					}
					want := p.Text
					if p.Filename == tt.post {
						want = lastSave
					}
					if got.Filename != p.Filename || got.Text != want {
						t.Errorf("%s = %s %q, want %q", p.Filename, got.Filename, got.Text, want)
					}
				}
				return
			}
			got, err := posts.FindPostWithFilename("b.md", "user-1", "prose")
			if err != nil {
				t.Fatal(err)
			}
			want := "# a"
			if tt.post == "a.md" {
				want = lastSave
			}
			if got.ID != a.ID || got.Text != want || !got.Hidden {
				t.Errorf("b.md = %s %q hidden %v, want %s %q hidden", got.ID, got.Text, got.Hidden, a.ID, want)
			}
		})
	}
}

func TestPostRenameMovedDestination(t *testing.T) {
	session, posts := newPostsSession(t)
	a := insertPost(t, posts, "a.md", "# a", false)
	b := insertPost(t, posts, "b.md", "# b", false)
	moved := false
	move := func() {
		if moved {
			return
		}
		moved = true
		cur, err := posts.FindPost(b.ID)
		if err != nil {
			t.Fatal(err)
		}
		next := *cur
		next.Filename, next.Slug = "f.md", "f"
		if _, err := posts.RenamePost(cur, &next, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	h := NewScpPostHandler(posts, &shared.ConfigSite{Space: "prose"}, extHooks{meta: move})
	if err := h.Rename(session, &sendutils.FileEntry{Filepath: "/a.md"}, &sendutils.FileEntry{Filepath: "/b.md"}); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"b.md": a.ID, "f.md": b.ID} {
		if got, err := posts.FindPostWithFilename(name, "user-1", "prose"); err != nil || got.ID != id {
			t.Errorf("%s = %+v, %v, want post %s", name, got, err, id)
		}
	}
}
