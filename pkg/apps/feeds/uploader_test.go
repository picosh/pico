package feeds

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"golang.org/x/crypto/ssh"
)

func TestWriteKeepsDigestState(t *testing.T) {
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
	cfg := NewConfig("feeds-test")
	h := NewUploadHandler(cfg, posts)
	write := func(text string, mtime int64) {
		t.Helper()
		_, err := h.Write(session, &sendutils.FileEntry{Filepath: "/news.txt", Reader: strings.NewReader(text), Mtime: mtime})
		if err != nil {
			t.Fatal(err)
		}
	}

	write("=: email me@pico.test\n=> https://pico.sh/rss.xml\n", 100)
	post, err := posts.FindPostWithFilename("news.txt", user.ID, cfg.Space)
	if err != nil {
		t.Fatal(err)
	}
	digest := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	post.Data = db.PostData{LastDigest: &digest, Attempts: 2}
	if _, err := posts.UpdatePost(post); err != nil {
		t.Fatal(err)
	}

	write("=: email me@pico.test\n=> https://blog.pico.sh/rss.xml\n", 200)
	got, err := posts.FindPostWithFilename("news.txt", user.ID, cfg.Space)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "blog.pico.sh") || got.Data.LastDigest == nil || !got.Data.LastDigest.Equal(digest) || got.Data.Attempts != 2 {
		t.Errorf("feed = %q with data %+v, want the new text and the old digest state", got.Text, got.Data)
	}

	_, err = h.Write(session, &sendutils.FileEntry{Filepath: "/news.txt", Reader: strings.NewReader("=> https://pico.sh/rss.xml\n")})
	if err == nil || !strings.Contains(err.Error(), "no email") {
		t.Errorf("feed without an email: %v", err)
	}
}
