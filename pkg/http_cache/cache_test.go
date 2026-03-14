package http_cache

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestContext holds shared test state.
type TestContext struct {
	t            *testing.T
	handler      http.Handler
	cachedServer *httptest.Server
}

// NewTestContext creates a test context with a backend and cached server.
func NewTestContext(t *testing.T, cacheHandler http.Handler) *TestContext {
	tc := &TestContext{
		t:       t,
		handler: cacheHandler,
	}

	tc.cachedServer = httptest.NewServer(tc.handler)
	t.Cleanup(tc.cachedServer.Close)

	return tc
}

func (tc *TestContext) Get(path string) (*http.Response, error) {
	req, _ := http.NewRequest("GET", tc.cachedServer.URL+path, nil)
	return http.DefaultClient.Do(req)
}

func (tc *TestContext) GetWithHeaders(path string, headers map[string][]string) (*http.Response, error) {
	req, _ := http.NewRequest("GET", tc.cachedServer.URL+path, nil)
	for key, val := range headers {
		for _, v := range val {
			req.Header.Add(key, v)
		}
	}
	return http.DefaultClient.Do(req)
}

func (tc *TestContext) GetHeader(resp *http.Response, key string) string {
	return resp.Header.Get(key)
}

// RFC 5.1 Response Age.
// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.1
// RFC 4.2.3 Calculating Age.
// https://www.rfc-editor.org/rfc/rfc9111.html#section-4.2.3
func TestAge(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("success"))
	})

	logger := slog.Default()
	handler := NewPicoCacheHandler(logger, mux)
	tc := NewTestContext(t, handler)

	// first request hits backend
	resp1, _ := tc.Get("/test")
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp1.StatusCode)
	}
	age1 := resp1.Header.Get("Age")
	if age1 != "" {
		t.Errorf("expected empty, got %s", age1)
	}

	time.Sleep(time.Second * 1)

	// second request hits cache
	resp2, _ := tc.Get("/test")
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp1.StatusCode)
	}
	age2 := resp2.Header.Get("Age")
	ageNum, err := strconv.Atoi(age2)
	if err != nil {
		t.Fatalf("invalide Age header %s", err)
	}
	if ageNum == 0 {
		t.Errorf("expected non-zero, got %d", ageNum)
	}
}

// RFC 5.2.1.4 Request Cache-Control: no-cache
// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.4
func TestNoCache(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("success"))
	})

	logger := slog.Default()
	handler := NewPicoCacheHandler(logger, mux)
	tc := NewTestContext(t, handler)

	// first request hits backend
	resp1, _ := tc.Get("/test")
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp1.StatusCode)
	}

	// second request hits cache
	resp2, _ := tc.GetWithHeaders("/test", map[string][]string{"Cache-Control": {"no-cache"}})
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp1.StatusCode)
	}
	status := resp2.Header.Get("cache-status")
	if !strings.Contains(status, "miss") {
		t.Errorf("expected miss, got %s", status)
	}
}

// RFC 5.2.1.1 Request Cache-Control: max-age.
// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.1
func TestMaxAge(t *testing.T) {

}
