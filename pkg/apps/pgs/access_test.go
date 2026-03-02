package pgs

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/storage"
)

func TestPrivateProjectDeniesWebAccess(t *testing.T) {
	logger := slog.Default()
	dbpool := NewPgsDb(logger)
	bucketName := shared.GetAssetBucketName(dbpool.Users[0].ID)

	// Mark the test project as private
	project, err := dbpool.FindProjectByName(dbpool.Users[0].ID, "test")
	if err != nil {
		t.Fatalf("failed to get project: %v", err)
	}
	project.Acl.Type = "private"
	project.Acl.Data = []string{}

	request := httptest.NewRequest("GET", "https://"+dbpool.Users[0].Name+"-test.pgs.test/", strings.NewReader(""))
	responseRecorder := httptest.NewRecorder()

	st, _ := storage.NewStorageMemory(map[string]map[string]string{
		bucketName: {
			"/test/index.html": "hello world!",
		},
	})
	pubsub := NewPubsubChan()
	defer func() {
		_ = pubsub.Close()
	}()
	cfg := NewPgsConfig(logger, dbpool, st, pubsub)
	cfg.Domain = "pgs.test"
	router := NewWebRouter(cfg)
	router.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusUnauthorized {
		t.Errorf("want status %d, got %d", http.StatusUnauthorized, responseRecorder.Code)
	}
}
