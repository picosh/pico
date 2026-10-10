package pastes

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
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
	cfg := NewConfigSite("pastes-test")
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
			handler := NewUploadHandler(cfg, mockDB, map[string]filehandlers.ReadWriteHandler{})

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
