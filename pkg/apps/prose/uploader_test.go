package prose

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
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
