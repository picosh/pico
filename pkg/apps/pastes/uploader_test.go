package pastes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
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
	if ff, ok := m.features[userID+":"+name]; ok {
		return ff, nil
	}
	return nil, fmt.Errorf("feature flag %s not found for user %s", name, userID)
}

func (m *mockFeatureDB) setFeature(userID, name string, expiresAt time.Time) {
	m.features[userID+":"+name] = &db.FeatureFlag{UserID: userID, Name: name, ExpiresAt: &expiresAt}
}

func TestUploadHandlerValidate(t *testing.T) {
	cfg := NewConfig("pastes-test")
	userID := "user-1"
	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)

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

	tests := []struct {
		name    string
		setup   func(m *mockFeatureDB)
		user    *db.User
		wantErr string
	}{
		{
			name:    "nil user",
			setup:   func(m *mockFeatureDB) {},
			wantErr: "could not get user",
		},
		{
			name:    "no invite or pico+",
			setup:   func(m *mockFeatureDB) {},
			user:    &db.User{ID: userID, Name: "tester"},
			wantErr: "requires an invitation or pico+",
		},
		{
			name:  "pastes feature",
			setup: func(m *mockFeatureDB) { m.setFeature(userID, "pastes", future) },
			user:  &db.User{ID: userID, Name: "tester"},
		},
		{
			name:  "pico+ feature",
			setup: func(m *mockFeatureDB) { m.setFeature(userID, "plus", future) },
			user:  &db.User{ID: userID, Name: "tester"},
		},
		{
			name:  "expired pico+ with active pastes",
			setup: func(m *mockFeatureDB) { m.setFeature(userID, "plus", past); m.setFeature(userID, "pastes", future) },
			user:  &db.User{ID: userID, Name: "tester"},
		},
		{
			name:    "expired pastes",
			setup:   func(m *mockFeatureDB) { m.setFeature(userID, "pastes", past) },
			user:    &db.User{ID: userID, Name: "tester"},
			wantErr: "pastes access has expired",
		},
		{
			name:    "expired pico+",
			setup:   func(m *mockFeatureDB) { m.setFeature(userID, "plus", past) },
			user:    &db.User{ID: userID, Name: "tester"},
			wantErr: "pico+ has expired",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB := newMockFeatureDB()
			tt.setup(mockDB)
			handler := NewUploadHandler(cfg, mockDB)

			err := handler.Validate(createSession(tt.user))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestSetMetaKeepsSettings(t *testing.T) {
	soon := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	// Pastes expire after calendar days, which a daylight saving change
	// makes an hour longer or shorter.
	defaultExpiry := time.Until(time.Now().AddDate(0, 0, DEFAULT_EXPIRES_AT)).Round(time.Minute)
	tests := []struct {
		name   string
		cmd    []string
		cur    *db.Post
		hidden bool
		// expires is how long until the paste expires, or 0 for never.
		expires time.Duration
	}{
		{name: "new", expires: defaultExpiry},
		{name: "new with arguments", cmd: []string{"hidden=true", "expires=2h"}, hidden: true, expires: 2 * time.Hour},
		{name: "existing", cur: &db.Post{Hidden: true, ExpiresAt: &soon}, hidden: true, expires: time.Hour},
		{name: "existing without expiry", cur: &db.Post{Hidden: true}, hidden: true},
		{name: "existing past its expiry", cur: &db.Post{Hidden: true, ExpiresAt: &past}, hidden: true, expires: defaultExpiry},
		{name: "existing with arguments", cmd: []string{"hidden=false", "expires=true"}, cur: &db.Post{Hidden: true}, expires: defaultExpiry},
		{name: "existing made permanent", cmd: []string{"expires=false"}, cur: &db.Post{ExpiresAt: &soon}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &pssh.SSHServerConnSession{Ctx: context.Background()}
			s.SetValue("command", tt.cmd)
			data := &db.Post{Filename: "a.txt", Slug: "a"}
			setMeta(s, tt.cur, data)
			if data.Hidden != tt.hidden {
				t.Errorf("hidden = %v, want %v", data.Hidden, tt.hidden)
			}
			switch {
			case tt.expires == 0 && data.ExpiresAt != nil:
				t.Errorf("expires at %v, want never", data.ExpiresAt)
			case tt.expires != 0 && (data.ExpiresAt == nil || time.Until(*data.ExpiresAt).Round(time.Minute) != tt.expires):
				t.Errorf("expires at %v, want in %v", data.ExpiresAt, tt.expires)
			}
		})
	}
}

func TestWriteReadError(t *testing.T) {
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
			user := &db.User{ID: "user-1", Name: "tester"}
			pssh.SetUser(session, user)
			posts := stub.NewPostsDB(logger, user)
			handler := NewUploadHandler(&Config{Space: "pastes", MaxAssetSize: 1024}, posts)
			reader := io.MultiReader(strings.NewReader(tt.text), iotest.ErrReader(tt.err))

			message, err := handler.Write(session, &sendutils.FileEntry{Filepath: "/test.txt", Reader: reader})
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
			if message != "" {
				t.Fatalf("failed upload returned a success message: %q", message)
			}
			if all, _ := posts.FindAllPostsByUser(user.ID, "pastes"); len(all) != 0 {
				t.Fatalf("failed upload stored %d posts", len(all))
			}
		})
	}
}
