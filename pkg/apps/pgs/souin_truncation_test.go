package pgs

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/storage"
)

// TestLargeFileNotTruncatedOnCacheHit reproduces the Souin truncation bug.
// Large files (3MB) are cached as truncated (~4KB) on the second request.
//
// Bug behavior:
// - First request (cache miss): Returns full 3MB file ✓
// - Second request (cache hit): Returns only ~4KB (truncated!) ✗
//
// Root cause: Souin's Store() snapshots the buffer mid-stream while
// io.Copy() is still writing data, resulting in partial cache entries.
func TestLargeFileNotTruncatedOnCacheHit(t *testing.T) {
	logger := slog.Default()
	dbpool := NewPgsDb(logger)
	bucketName := shared.GetAssetBucketName(dbpool.Users[0].ID)

	// 3MB payload - reproduces the exact bug scenario
	largePayload := bytes.Repeat([]byte("x"), 3*1024*1024)
	expectedSize := len(largePayload)

	st, err := storage.NewStorageMemory(map[string]map[string]string{
		bucketName: {
			"/test/large-file.bin": string(largePayload),
		},
	})
	if err != nil {
		t.Fatalf("storage setup failed: %v", err)
	}

	pubsub := NewPubsubChan()
	defer func() {
		_ = pubsub.Close()
	}()

	cfg := NewPgsConfig(logger, dbpool, st, pubsub)
	cfg.Domain = "pgs.test"
	// Increase max asset size for testing large files
	cfg.MaxAssetSize = 10 * 1024 * 1024 // 10MB

	// Set up the full stack WITH Souin HTTP caching middleware
	httpCache := SetupCache(cfg)
	routes := NewWebRouter(cfg)
	cacher := &CachedHttp{
		handler: httpCache,
		routes:  routes,
	}

	// First request (cache miss)
	req1 := httptest.NewRequest("GET", dbpool.mkpath("/large-file.bin"), strings.NewReader(""))
	rec1 := httptest.NewRecorder()
	cacher.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("first request failed with status %d", rec1.Code)
	}

	body1 := rec1.Body.String()
	size1 := len(body1)

	if size1 != expectedSize {
		t.Errorf("first request: expected %d bytes, got %d bytes", expectedSize, size1)
	}

	t.Logf("Cache miss: received %d bytes (expected %d)", size1, expectedSize)

	// Second request (cache hit) - This is where the bug manifests
	req2 := httptest.NewRequest("GET", dbpool.mkpath("/large-file.bin"), strings.NewReader(""))
	rec2 := httptest.NewRecorder()
	cacher.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("second request failed with status %d", rec2.Code)
	}

	body2 := rec2.Body.String()
	size2 := len(body2)

	t.Logf("Cache hit: received %d bytes (expected %d)", size2, expectedSize)

	// CRITICAL ASSERTION: Both requests must return the full file
	if size2 != expectedSize {
		t.Errorf("SOUIN_TRUNCATION_BUG: cache hit returned %d bytes instead of %d bytes",
			size2, expectedSize)

		// Show evidence of truncation
		if size2 < 10000 {
			t.Logf("Truncated response: only %d bytes (about %dKB)", size2, size2/1024)
		}
	}

	// Verify both responses are identical
	if body1 != body2 {
		t.Errorf("cache hit response differs from cache miss")
		t.Errorf("Cache miss: %d bytes, Cache hit: %d bytes", size1, size2)
	}
}

// TestMediumFileNotTruncatedOnCacheHit tests with 512KB files
// which can trigger the race condition due to buffering behavior.
func TestMediumFileNotTruncatedOnCacheHit(t *testing.T) {
	logger := slog.Default()
	dbpool := NewPgsDb(logger)
	bucketName := shared.GetAssetBucketName(dbpool.Users[0].ID)

	// 512KB payload with repeating pattern
	payload := bytes.Repeat([]byte("0123456789ABCDEF"), 512*1024/16)
	expectedSize := len(payload)

	st, err := storage.NewStorageMemory(map[string]map[string]string{
		bucketName: {
			"/test/medium-file.bin": string(payload),
		},
	})
	if err != nil {
		t.Fatalf("storage setup failed: %v", err)
	}

	pubsub := NewPubsubChan()
	defer func() {
		_ = pubsub.Close()
	}()

	cfg := NewPgsConfig(logger, dbpool, st, pubsub)
	cfg.Domain = "pgs.test"
	// Increase max asset size for testing large files
	cfg.MaxAssetSize = 10 * 1024 * 1024 // 10MB

	httpCache := SetupCache(cfg)
	routes := NewWebRouter(cfg)
	cacher := &CachedHttp{
		handler: httpCache,
		routes:  routes,
	}

	// First request (cache miss)
	req1 := httptest.NewRequest("GET", dbpool.mkpath("/medium-file.bin"), strings.NewReader(""))
	rec1 := httptest.NewRecorder()
	cacher.ServeHTTP(rec1, req1)

	body1 := rec1.Body.String()
	size1 := len(body1)

	// Second request (cache hit)
	req2 := httptest.NewRequest("GET", dbpool.mkpath("/medium-file.bin"), strings.NewReader(""))
	rec2 := httptest.NewRecorder()
	cacher.ServeHTTP(rec2, req2)

	body2 := rec2.Body.String()
	size2 := len(body2)

	t.Logf("Cache miss: %d bytes, Cache hit: %d bytes (expected %d)", size1, size2, expectedSize)

	// Verify complete responses
	if size1 != expectedSize {
		t.Errorf("cache miss: expected %d bytes, got %d", expectedSize, size1)
	}

	if size2 != expectedSize {
		t.Errorf("SOUIN_TRUNCATION_BUG: cache hit returned %d bytes instead of %d bytes",
			size2, expectedSize)
	}

	// Verify content matches original
	if body1 != string(payload) {
		t.Errorf("cache miss response body doesn't match")
	}

	if body2 != string(payload) {
		t.Errorf("cache hit response body doesn't match")
	}
}
