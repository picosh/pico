package filehandlers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
	"golang.org/x/crypto/ssh"
)

type uploadHooks struct {
	validated bool
}

func (h *uploadHooks) FileValidate(*pssh.SSHServerConnSession, *PostMetaData) (bool, error) {
	h.validated = true
	return false, errors.New("unexpected validation after a failed read")
}

func (h *uploadHooks) FileMeta(*pssh.SSHServerConnSession, *PostMetaData) error {
	return nil
}

func TestPostUploadReadError(t *testing.T) {
	checksumErr := errors.New("rsync: file failed verification")
	tests := []struct {
		name string
		text string
		err  error
	}{
		{name: "before contents", err: io.ErrClosedPipe},
		{name: "partial contents", text: "partial upload", err: io.ErrUnexpectedEOF},
		{name: "failed verification", text: "complete upload", err: checksumErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.DiscardHandler)
			session := &pssh.SSHServerConnSession{
				SSHServerConn: &pssh.SSHServerConn{
					Conn:   &ssh.ServerConn{Permissions: &ssh.Permissions{Extensions: map[string]string{}}},
					Logger: logger,
				},
				Ctx: context.Background(),
			}
			pssh.SetUser(session, &db.User{ID: "user-1", Name: "tester"})
			hooks := &uploadHooks{}
			handler := NewScpPostHandler(stub.NewStubDB(logger), &shared.ConfigSite{Space: "pastes"}, hooks)
			reader := io.MultiReader(strings.NewReader(tt.text), iotest.ErrReader(tt.err))

			message, err := handler.Write(session, &sendutils.FileEntry{Filepath: "/test.txt", Reader: reader})
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
			if message != "" {
				t.Fatalf("failed upload returned a success message: %q", message)
			}
			if hooks.validated {
				t.Fatal("failed upload reached validation")
			}
		})
	}
}
