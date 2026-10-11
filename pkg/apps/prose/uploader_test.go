package prose

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
	"golang.org/x/crypto/ssh"
)

type mockFeatureDB struct {
	*stub.StubDB
	features map[string]*db.FeatureFlag
}

func newMockFeatureDB() *mockFeatureDB {
	return &mockFeatureDB{
		StubDB:   stub.NewStubDB(slog.Default()),
		features: make(map[string]*db.FeatureFlag),
	}
}

func (m *mockFeatureDB) FindFeature(userID, name string) (*db.FeatureFlag, error) {
	key := userID + ":" + name
	if ff, ok := m.features[key]; ok {
		return ff, nil
	}
	return nil, fmt.Errorf("feature flag %s not found for user %s", name, userID)
}

func (m *mockFeatureDB) setFeature(userID string, ff *db.FeatureFlag) {
	key := userID + ":" + ff.Name
	m.features[key] = ff
}

func TestUploadHandlerValidate(t *testing.T) {
	cfg := NewConfigSite("prose-test")
	validExpires := time.Now().Add(24 * time.Hour)
	userID := "user-1"

	createSession := func(user *db.User) *pssh.SSHServerConnSession {
		conn := &pssh.SSHServerConn{
			Conn: &ssh.ServerConn{
				Permissions: &ssh.Permissions{
					Extensions: map[string]string{},
				},
			},
			Logger: slog.Default(),
		}
		sesh := &pssh.SSHServerConnSession{
			SSHServerConn: conn,
			Ctx:           context.Background(),
		}
		if user != nil {
			pssh.SetUser(sesh, user)
		}
		return sesh
	}

	t.Run("nil user in session fails validation", func(t *testing.T) {
		mockDB := newMockFeatureDB()
		handler := NewUploadHandler(cfg, mockDB, map[string]filehandlers.ReadWriteHandler{})

		sesh := createSession(nil)
		err := handler.Validate(sesh)
		if err == nil {
			t.Fatalf("expected error for nil user, got nil")
		}
	})

	t.Run("user without feature flag fails validation", func(t *testing.T) {
		mockDB := newMockFeatureDB()
		handler := NewUploadHandler(cfg, mockDB, map[string]filehandlers.ReadWriteHandler{})

		user := &db.User{ID: userID, Name: "tester"}
		sesh := createSession(user)
		err := handler.Validate(sesh)
		if err == nil {
			t.Fatalf("expected error for user without feature flag, got nil")
		}
	})

	t.Run("user with prose feature flag passes validation and sets session flag", func(t *testing.T) {
		mockDB := newMockFeatureDB()
		proseFF := db.NewFeatureFlag(userID, "prose", uint64(50*shared.MB), int64(5*shared.MB), int64(2*shared.KB))
		proseFF.ExpiresAt = &validExpires
		mockDB.setFeature(userID, proseFF)

		handler := NewUploadHandler(cfg, mockDB, map[string]filehandlers.ReadWriteHandler{})

		user := &db.User{ID: userID, Name: "tester"}
		sesh := createSession(user)
		err := handler.Validate(sesh)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		ff := getFeatureFlag(sesh)
		if ff == nil || ff.Name != "prose" {
			t.Errorf("expected session feature flag 'prose', got %v", ff)
		}
	})
}

func TestPostWriteTagsAndAliases(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	session := &pssh.SSHServerConnSession{
		SSHServerConn: &pssh.SSHServerConn{
			Conn:   &ssh.ServerConn{Permissions: &ssh.Permissions{Extensions: map[string]string{}}},
			Logger: logger,
		},
		Ctx: context.Background(),
	}
	user := &db.User{ID: "user-1", Name: "tester"}
	pssh.SetUser(session, user)
	posts := stub.NewPostsDB(logger, user)
	cfg := NewConfigSite("prose-test")
	h := &postUploader{Cfg: cfg, DB: posts}
	write := func(text string, mtime int64) *db.Post {
		t.Helper()
		_, err := h.Write(session, &sendutils.FileEntry{Filepath: "/hello.md", Reader: strings.NewReader(text), Mtime: mtime})
		if err != nil {
			t.Fatal(err)
		}
		post, err := posts.FindPostWithFilename("hello.md", user.ID, cfg.Space)
		if err != nil {
			t.Fatal(err)
		}
		return post
	}

	post := write("---\ntitle: Hello\ntags: [a, b]\naliases: [hi]\n---\n# hello\n", 100)
	if post.Title != "Hello" || !slices.Equal(post.Tags, []string{"a", "b"}) || !slices.Equal(posts.Aliases(post.ID), []string{"hi"}) {
		t.Errorf("new post = %q tags %q aliases %q", post.Title, post.Tags, posts.Aliases(post.ID))
	}

	post = write("# hello again\n", 200)
	if len(post.Tags) != 0 || len(posts.Aliases(post.ID)) != 0 {
		t.Errorf("updated post kept tags %q and aliases %q", post.Tags, posts.Aliases(post.ID))
	}
}
