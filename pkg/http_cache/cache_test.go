package http_cache

import (
	"encoding/json"
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

func (tc *TestContext) Do(req *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(req)
}

func (tc *TestContext) DoWithHeaders(req *http.Request, headers map[string][]string) (*http.Response, error) {
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

func testCacheValue(afterCreated time.Duration) *CacheValue {
	return &CacheValue{
		Header:    map[string][]string{},
		Body:      []byte("success"),
		CreatedAt: time.Now().Add(afterCreated),
	}
}

/*
TODO:
	Storing incomplete responses 	https://www.rfc-editor.org/rfc/rfc9111.html#section-3.3
	Storing authenticated requests 	https://www.rfc-editor.org/rfc/rfc9111.html#section-3.5
	Expires 						https://www.rfc-editor.org/rfc/rfc9111.html#section-5.3
	Max-Age logic
*/

// RFC 9211 The Cache-Status HTTP Response Header Field
// https://www.rfc-editor.org/rfc/rfc9211#section-2
func TestCacheCacheStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("success"))
	})

	logger := slog.Default()
	handler := NewPicoCacheHandler(logger, mux)
	tc := NewTestContext(t, handler)
	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)

	// first request hits backend
	resp1, _ := tc.Do(req)
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp1.StatusCode)
	}
	status := resp1.Header.Get("cache-status")
	if !strings.Contains(status, "miss") {
		t.Errorf("expected miss, got %s", status)
	}

	// second request hits cache
	resp2, _ := tc.Do(req)
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
	status = resp2.Header.Get("cache-status")
	if !strings.Contains(status, "hit") {
		t.Errorf("expected hit, got %s", status)
	}
}

// RFC 9110 15.1-2 Heuristically Cacheable
// https://www.rfc-editor.org/rfc/rfc9110#section-15.1-2
// 200, 203, 204, 206, 300, 301, 308, 404, 405, 410, 414, and 501.
func TestCacheStatusCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("boom!"))
	})

	logger := slog.Default()
	handler := NewPicoCacheHandler(logger, mux)
	tc := NewTestContext(t, handler)
	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)

	// first request hits backend
	resp1, _ := tc.Do(req)
	if resp1.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", resp1.StatusCode)
	}
	status := resp1.Header.Get("cache-status")
	if !strings.Contains(status, "miss") {
		t.Errorf("expected miss, got %s", status)
	}

	// second request hits backend
	resp2, _ := tc.Do(req)
	if resp2.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", resp2.StatusCode)
	}
	status = resp2.Header.Get("cache-status")
	if !strings.Contains(status, "miss") {
		t.Errorf("expected miss, got %s", status)
	}
}

// RFC 9111 2.3 Opinion - Only store GET requests
// https://www.rfc-editor.org/rfc/rfc9111.html#section-2-3
func TestCacheMethod(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("success"))
	})

	logger := slog.Default()
	handler := NewPicoCacheHandler(logger, mux)
	tc := NewTestContext(t, handler)
	req, _ := http.NewRequest("POST", tc.cachedServer.URL+"/test", nil)

	// first request hits backend
	resp1, _ := tc.Do(req)
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp1.StatusCode)
	}
	status := resp1.Header.Get("cache-status")
	if !strings.Contains(status, "miss") {
		t.Errorf("expected miss, got %s", status)
	}

	// second request hits backend
	resp2, _ := tc.Do(req)
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
	status = resp2.Header.Get("cache-status")
	if !strings.Contains(status, "miss") {
		t.Errorf("expected miss, got %s", status)
	}
}

// RFC 9111 3.1 Storing Header and Trailer Fields
// https://www.rfc-editor.org/rfc/rfc9111.html#section-3.1
func TestCacheStoringHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("connection", "idk")
		w.Header().Set("proxy-authenticate", "idk")
		w.Header().Set("proxy-authentication-info", "idk")
		w.Header().Set("proxy-authorization", "idk")

		w.WriteHeader(200)
		_, _ = w.Write([]byte("success"))
	})

	logger := slog.Default()
	handler := NewPicoCacheHandler(logger, mux)
	tc := NewTestContext(t, handler)
	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)

	// first request hits backend
	resp1, _ := tc.Do(req)
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp1.StatusCode)
	}
	status := resp1.Header.Get("cache-status")
	if !strings.Contains(status, "miss") {
		t.Errorf("expected miss, got %s", status)
	}

	// second request hits cache
	resp2, _ := tc.Do(req)
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
	headers := []string{
		resp2.Header.Get("connection"),
		resp2.Header.Get("proxy-authenticate"),
		resp2.Header.Get("proxy-authentication-info"),
		resp2.Header.Get("proxy-authorization"),
	}
	for _, hdr := range headers {
		if hdr != "" {
			t.Errorf("expected no header, found one: %s", hdr)
		}
	}
}

// RFC 9111 4.1 Vary.
// https://www.rfc-editor.org/rfc/rfc9111.html#section-4.1
func TestCacheVary(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("success"))
	})

	logger := slog.Default()
	handler := NewPicoCacheHandler(logger, mux)
	tc := NewTestContext(t, handler)

	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)
	cacheKey := getCacheKey(req)
	cv := testCacheValue(250 * time.Second)
	cv.Header["Vary"] = []string{"Accept-Encoding"}
	cv.Header["Content-Encoding"] = []string{"gzip"}
	cacheValue, _ := json.Marshal(cv)
	handler.Cache.Add(cacheKey, cacheValue)

	respMatch, _ := tc.DoWithHeaders(req, map[string][]string{
		"Accept-Encoding": {"gzip"},
	})
	status := respMatch.Header.Get("cache-status")
	if !strings.Contains(status, "hit") {
		t.Errorf("expected hit, got %s", status)
	}

	respMisMatch, _ := tc.DoWithHeaders(req, map[string][]string{
		"Accept-Encoding": {"text/plain"},
	})
	status = respMisMatch.Header.Get("cache-status")
	if !strings.Contains(status, "miss") {
		t.Errorf("expected miss, got %s", status)
	}
}

// RFC 9111 4.3 Validation.
// https://www.rfc-editor.org/rfc/rfc9111.html#section-4.3
// Last-Modified ETag If-Match If-None-Match If-Range If-Modified-Since If-Unmodified-Since
// RFC 9110 13 Conditional Requests.
// https://www.rfc-editor.org/rfc/rfc9110#section-13
func TestCacheValidation(t *testing.T) {
	actual := time.Now().Add(-10 * time.Minute).UTC()
	actualStr := actual.Format(time.RFC1123)
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC1123)
	early := time.Now().Add(-20 * time.Minute)
	earlyStr := early.Format(time.RFC1123)
	tests := []struct {
		name             string
		link             string
		validationHeader string
		validationValue  string
		extraHeaders     map[string][]string
		expected         string
		originStatus     int
		expectedStatus   int
	}{
		{
			name:             "RFC 9110 13.1.2 If-None-Match",
			link:             "https://www.rfc-editor.org/rfc/rfc9110#section-13.1.2",
			validationHeader: "If-None-Match",
			validationValue:  "\"abc\"",
			expected:         "hit",
			originStatus:     http.StatusOK,
			expectedStatus:   http.StatusOK,
		},
		{
			name:             "RFC 9110 13.1.2 If-None-Match Wildcard",
			link:             "https://www.rfc-editor.org/rfc/rfc9110#section-13.1.2",
			validationHeader: "If-None-Match",
			validationValue:  "*",
			expected:         "hit",
			originStatus:     http.StatusOK,
			expectedStatus:   http.StatusNotModified,
		},
		{
			name:             "RFC 9110 13.1.3 If-Modified-Since",
			link:             "https://www.rfc-editor.org/rfc/rfc9110#section-13.1.3",
			validationHeader: "If-Modified-Since",
			validationValue:  nowStr,
			expected:         "hit",
			originStatus:     http.StatusOK,
			expectedStatus:   http.StatusNotModified,
		},
		{
			name:             "RFC 9110 13.1.4 If-Unmodified-Since",
			link:             "https://www.rfc-editor.org/rfc/rfc9110#section-13.1.4",
			validationHeader: "If-Unmodified-Since",
			validationValue:  earlyStr,
			expected:         "hit",
			originStatus:     http.StatusOK,
			expectedStatus:   http.StatusOK,
		},
		{
			name:             "RFC 9110 13.1.5 If-Range Date",
			link:             "https://www.rfc-editor.org/rfc/rfc9110#section-13.1.5",
			validationHeader: "If-Range",
			validationValue:  nowStr,
			extraHeaders: map[string][]string{
				"Range": {"bytes=0-3"},
			},
			expected:       "hit",
			originStatus:   http.StatusOK,
			expectedStatus: http.StatusOK,
		},
		{
			name:             "RFC 9110 13.1.5 If-Range Date Hit",
			link:             "https://www.rfc-editor.org/rfc/rfc9110#section-13.1.5",
			validationHeader: "If-Range",
			validationValue:  actualStr,
			extraHeaders: map[string][]string{
				"Range": {"bytes=0-3"},
			},
			expected:       "hit",
			originStatus:   http.StatusOK,
			expectedStatus: http.StatusOK,
		},
		{
			name:             "RFC 9110 13.1.5 If-Range ETag",
			link:             "https://www.rfc-editor.org/rfc/rfc9110#section-13.1.5",
			validationHeader: "If-Range",
			validationValue:  "\"abc\"",
			extraHeaders: map[string][]string{
				"Range": {"bytes=0-3"},
			},
			expected:       "hit",
			originStatus:   http.StatusOK,
			expectedStatus: http.StatusOK,
		},
		{
			name:             "RFC 9110 13.1.5 If-Range ETag Hit",
			link:             "https://www.rfc-editor.org/rfc/rfc9110#section-13.1.5",
			validationHeader: "If-Range",
			validationValue:  "\"ccc\"",
			extraHeaders: map[string][]string{
				"Range": {"bytes=0-3"},
			},
			expected:       "hit",
			originStatus:   http.StatusOK,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.originStatus)
		})

		logger := slog.Default()
		handler := NewPicoCacheHandler(logger, mux)
		handler.Ttl = time.Minute * 10
		tc := NewTestContext(t, handler)

		req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)
		cacheKey := getCacheKey(req)
		cv := testCacheValue(250 * time.Second)
		cv.Header["ETag"] = []string{"ccc"}
		cv.Header["Last-Modified"] = []string{actualStr}
		cacheValue, _ := json.Marshal(cv)
		handler.Cache.Add(cacheKey, cacheValue)

		t.Run(tt.name, func(t *testing.T) {
			reqHeaders := map[string][]string{tt.validationHeader: {tt.validationValue}}
			for key, values := range tt.extraHeaders {
				reqHeaders[key] = values
			}

			resp, _ := tc.DoWithHeaders(req, reqHeaders)
			actual := resp.Header.Get("cache-status")
			if !strings.Contains(actual, tt.expected) {
				t.Errorf("expected %s, got %s\n%s", tt.expected, actual, tt.link)
			}
			if resp.StatusCode != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, resp.StatusCode)
			}
		})
	}
}

// RFC 9111 5.1 Age.
// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.1
// RFC 9111 4.2.3 Calculating Age.
// https://www.rfc-editor.org/rfc/rfc9111.html#section-4.2.3
func TestCacheAge(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("success"))
	})

	logger := slog.Default()
	handler := NewPicoCacheHandler(logger, mux)
	tc := NewTestContext(t, handler)

	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)
	cacheKey := getCacheKey(req)
	cacheValue, _ := json.Marshal(testCacheValue(250 * time.Second))
	handler.Cache.Add(cacheKey, cacheValue)

	resp, _ := tc.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	age := resp.Header.Get("age")
	ageNum, err := strconv.Atoi(age)
	if err != nil {
		t.Fatalf("invalide age header %s", err)
	}
	if ageNum == 0 {
		t.Errorf("expected non-zero, got %d", ageNum)
	}
}

// RFC 9111 5.2.1 Request Directives
// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1
func TestCacheRequestDirectives(t *testing.T) {
	tests := []struct {
		name         string
		link         string
		cacheControl string
		expected     string
	}{
		{
			name:         "RFC 9111 5.2.1.1 Request Cache-Control: max-age",
			link:         "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.1",
			cacheControl: "max-age=100",
			expected:     "miss",
		},
		{
			name:         "RFC 9111 5.2.1.2 Request Cache-Control: max-stale",
			link:         "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.2",
			cacheControl: "max-stale=100",
			expected:     "miss",
		},
		{
			name:         "RFC 9111 5.2.1.3 Request Cache-Control: min-fresh",
			link:         "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.3",
			cacheControl: "min-fresh=400", // 600 ttl - 250 age = 350 freshness
			expected:     "miss",
		},
		{
			name:         "RFC 9111 5.2.1.4 Request Cache-Control: no-cache",
			link:         "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.4",
			cacheControl: "no-cache",
			expected:     "miss",
		},
		{
			name:         "RFC 9111 5.2.1.5 Request Cache-Control: no-store",
			link:         "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.5",
			cacheControl: "no-store",
			// you can reply with the cached version, just cannot store or update the response in
			//   the cache.
			expected: "hit",
		},
		{
			name:         "RFC 9111 5.2.1.6 Request Cache-Control: no-transform",
			link:         "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.6",
			cacheControl: "no-transform",
			expected:     "miss",
		},
		{
			name:         "RFC 9111 5.2.1.7 Request Cache-Control: only-if-cached",
			link:         "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.7",
			cacheControl: "only-if-cached",
			expected:     "hit",
		},
	}

	for _, tt := range tests {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			_, _ = w.Write([]byte("success"))
		})

		logger := slog.Default()
		handler := NewPicoCacheHandler(logger, mux)
		handler.Ttl = time.Minute * 10
		tc := NewTestContext(t, handler)

		req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)
		cacheKey := getCacheKey(req)
		cacheValue, _ := json.Marshal(testCacheValue(250 * time.Second))
		handler.Cache.Add(cacheKey, cacheValue)

		t.Run(tt.name, func(t *testing.T) {
			resp, _ := tc.DoWithHeaders(req, map[string][]string{"Cache-Control": {tt.cacheControl}})
			actual := resp.Header.Get("cache-status")
			if !strings.Contains(actual, tt.expected) {
				t.Errorf("expected %s, got %s\n%s", tt.expected, actual, tt.link)
			}
		})
	}
}

// RFC 9111 5.2.2 Response Directives
// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2
// These tests simply confirm that the response generated from origin server corresponds to the
// correct cache-control, http status code, and is "revalidated" the correct number of times.
// It does **not** validate the correct cache control logic like cache using max-age from origin
// server.
func TestCacheResponseDirectivesHasCacheControl(t *testing.T) {
	tests := []struct {
		name                      string
		link                      string
		cacheControl              string
		expectedOriginCalls       int
		expectedSecondCacheStatus string
	}{
		{
			name:                      "RFC 9111 5.2.2.1 Response Cache-Control max-age",
			link:                      "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.1",
			cacheControl:              "max-age=100",
			expectedOriginCalls:       1,
			expectedSecondCacheStatus: "hit",
		},
		{
			name:                      "RFC 9111 5.2.2.2 Response Cache-Control must-revalidate",
			link:                      "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.2",
			cacheControl:              "must-revalidate",
			expectedOriginCalls:       1,
			expectedSecondCacheStatus: "hit",
		},
		{
			name:                      "RFC 9111 5.2.2.3 Response Cache-Control must-understand",
			link:                      "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.3",
			cacheControl:              "must-understand",
			expectedOriginCalls:       1,
			expectedSecondCacheStatus: "hit",
		},
		{
			name:                      "RFC 9111 5.2.2.4 Response Cache-Control no-cache",
			link:                      "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.4",
			cacheControl:              "no-cache",
			expectedOriginCalls:       2,
			expectedSecondCacheStatus: "miss",
		},
		{
			name:                      "RFC 9111 5.2.2.5 Response Cache-Control no-store",
			link:                      "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.5",
			cacheControl:              "no-store",
			expectedOriginCalls:       2,
			expectedSecondCacheStatus: "miss",
		},
		{
			name:                      "RFC 9111 5.2.2.6 Response Cache-Control no-transform",
			link:                      "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.6",
			cacheControl:              "no-transform",
			expectedOriginCalls:       1,
			expectedSecondCacheStatus: "hit",
		},
		{
			name:                      "RFC 9111 5.2.2.7 Response Cache-Control private",
			link:                      "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.7",
			cacheControl:              "private",
			expectedOriginCalls:       1,
			expectedSecondCacheStatus: "hit",
		},
		{
			name:                      "RFC 9111 5.2.2.8 Response Cache-Control proxy-revalidate",
			link:                      "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.8",
			cacheControl:              "proxy-revalidate",
			expectedOriginCalls:       1,
			expectedSecondCacheStatus: "hit",
		},
		{
			name:                      "RFC 9111 5.2.2.9 Response Cache-Control public",
			link:                      "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.9",
			cacheControl:              "public",
			expectedOriginCalls:       1,
			expectedSecondCacheStatus: "hit",
		},
		{
			name:                      "RFC 9111 5.2.2.10 Response Cache-Control s-maxage",
			link:                      "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.10",
			cacheControl:              "s-maxage=100",
			expectedOriginCalls:       1,
			expectedSecondCacheStatus: "hit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			actualOriginCalls := 0
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				actualOriginCalls += 1
				w.Header().Set("cache-control", tt.cacheControl)
				w.WriteHeader(200)
				_, _ = w.Write([]byte("success"))
			})

			logger := slog.Default()
			handler := NewPicoCacheHandler(logger, mux)
			handler.Ttl = time.Minute * 10
			tc := NewTestContext(t, handler)

			req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)

			// first request hits backend
			resp1, _ := tc.Do(req)
			status := resp1.Header.Get("cache-status")
			if !strings.Contains(status, "miss") {
				t.Errorf("expected miss, got %s", status)
			}

			// second request can be served from cache or forwarded depending on directive
			resp2, _ := tc.Do(req)

			actualCc := resp2.Header.Get("cache-control")
			if actualCc != tt.cacheControl {
				t.Errorf("expected cache-control %s, got %s", tt.cacheControl, actualCc)
			}
			status = resp2.Header.Get("cache-status")
			if tt.expectedSecondCacheStatus != "" && !strings.Contains(status, tt.expectedSecondCacheStatus) {
				t.Errorf("expected %s, got %s", tt.expectedSecondCacheStatus, status)
			}
			if tt.expectedOriginCalls != actualOriginCalls {
				t.Errorf("expected %d origin calls, got %d", tt.expectedOriginCalls, actualOriginCalls)
			}
		})
	}
}
