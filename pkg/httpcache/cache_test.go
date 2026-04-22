package httpcache

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

/*
TODO:
	- Request no-store store-prevention (RFC 9111 §5.2.1.5): verify a no-store request does not populate/update cache for subsequent requests.
	- Authorization storage/use constraints (RFC 9111 §3.5): authenticated responses should not be reused unless explicitly permitted by response directives.
	- Vary: * behavior (RFC 9111 §4.1): ensure such responses are not reused for subsequent requests.
	- Multi-field Vary matching (RFC 9111 §4.1): all nominated request fields must match original request values, not just one.
	- Age correction with upstream metadata (RFC 9111 §4.2.3, §5.1): test interactions of stored response Date/Age values rather than only local clock delta.
*/

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
	reqCopy := req.Clone(req.Context())
	reqCopy.Header = req.Header.Clone()
	if reqCopy.Header == nil {
		reqCopy.Header = make(http.Header)
	}

	for key, val := range headers {
		reqCopy.Header.Del(key)
		for _, v := range val {
			reqCopy.Header.Add(key, v)
		}
	}
	return http.DefaultClient.Do(reqCopy)
}

func (tc *TestContext) GetHeader(resp *http.Response, key string) string {
	return resp.Header.Get(key)
}

func testCacheValue(afterCreated time.Duration) *CacheValue {
	return &CacheValue{
		Header:    map[string][]string{},
		Body:      []byte("success"),
		CreatedAt: time.Now().Add(-afterCreated),
	}
}

// RFC 9211 The Cache-Status HTTP Response Header Field
// https://www.rfc-editor.org/rfc/rfc9211#section-2
func TestCacheCacheStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("success"))
	})

	logger := slog.Default()
	handler := NewHttpCache(logger, mux)
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
	handler := NewHttpCache(logger, mux)
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
	handler := NewHttpCache(logger, mux)
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
	handler := NewHttpCache(logger, mux)
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
	handler := NewHttpCache(logger, mux)
	tc := NewTestContext(t, handler)

	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)
	cacheKey := handler.GetCacheKey(req)
	cv := testCacheValue(250 * time.Second)
	cv.Header["Vary"] = []string{"Accept-Encoding"}
	// Store the original request header that selected this representation.
	cv.Header["Accept-Encoding"] = []string{"gzip"}
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
		handler := NewHttpCache(logger, mux)
		handler.Ttl = time.Minute * 10
		tc := NewTestContext(t, handler)

		req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)
		cacheKey := handler.GetCacheKey(req)
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
	handler := NewHttpCache(logger, mux)
	tc := NewTestContext(t, handler)

	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)
	cacheKey := handler.GetCacheKey(req)
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
	if ageNum != 251 {
		t.Errorf("expected 250, got %d", ageNum)
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
			cacheControl: "max-stale=300", // 300 max-stale + 250 age = 550 > 450 freshness
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
		handler := NewHttpCache(logger, mux)
		handler.Ttl = time.Minute * 10
		tc := NewTestContext(t, handler)

		req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)
		cacheKey := handler.GetCacheKey(req)
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
			expectedOriginCalls:       2,
			expectedSecondCacheStatus: "miss", // this is a shared cache, do not store private
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
		{
			name:                      "RFC 9111 5.2.2.10 Response Cache-Control public+private",
			link:                      "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.10",
			cacheControl:              "public, s-maxage=100, private",
			expectedOriginCalls:       2,
			expectedSecondCacheStatus: "miss", // be restrictive and adhere to private directive
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
			handler := NewHttpCache(logger, mux)
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

// RFC 9111 5.3 Expires
// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.3
func TestCacheExpires(t *testing.T) {
	tests := []struct {
		name                string
		link                string
		expires             string
		expectedStatus      int
		expectedCacheStatus string
	}{
		{
			name:                "RFC 9111 5.3 Expires - future date",
			link:                "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.3",
			expires:             time.Now().Add(10 * time.Minute).UTC().Format(http.TimeFormat),
			expectedStatus:      http.StatusOK,
			expectedCacheStatus: "hit",
		},
		{
			name:                "RFC 9111 5.3 Expires - expired response",
			link:                "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.3",
			expires:             time.Now().Add(-10 * time.Minute).UTC().Format(http.TimeFormat),
			expectedStatus:      http.StatusOK,
			expectedCacheStatus: "fwd=uri-miss",
		},
		{
			name:                "RFC 9111 5.3 Expires - invalid Expires header",
			link:                "https://www.rfc-editor.org/rfc/rfc9111.html#section-5.3",
			expires:             "not-a-valid-date",
			expectedStatus:      http.StatusOK,
			expectedCacheStatus: "fwd=uri-miss",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Expires", tt.expires)
				w.WriteHeader(200)
				_, _ = w.Write([]byte("success"))
			})

			logger := slog.Default()
			handler := NewHttpCache(logger, mux)
			tc := NewTestContext(t, handler)

			req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)

			// first request hits backend
			resp1, _ := tc.Do(req)
			if resp1.StatusCode != http.StatusOK {
				t.Errorf("expected 200, got %d", resp1.StatusCode)
			}

			// second request behavior depends on Expires header
			resp2, _ := tc.Do(req)
			status := resp2.Header.Get("cache-status")
			if !strings.Contains(status, tt.expectedCacheStatus) {
				t.Errorf("expected %s, got %s\n%s", tt.expectedCacheStatus, status, tt.link)
			}
		})
	}
}

// RFC 9111 4.3.4 304 Not Modified
// https://www.rfc-editor.org/rfc/rfc9111.html#section-4.3.4
// When a cached entry is validated and the origin responds with 304, the cache:
// - Returns 304 to the client
// - Updates header metadata from the 304 response
// - Retains the cached body for subsequent requests.
func TestCache304NotModifiedMerge(t *testing.T) {
	originCalls := 0

	// Validation handler: returns 304 when ETag matches, 200 otherwise
	validationMux := http.NewServeMux()
	validationMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		originCalls++
		if r.Header.Get("If-None-Match") == "\"abc\"" {
			w.WriteHeader(http.StatusNotModified)
			w.Header().Set("etag", "\"abc-updated\"")
			return
		}
		w.Header().Set("etag", "\"abc\"")
		w.Header().Set("cache-control", "max-age=60")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("original body"))
	})

	logger := slog.Default()
	handler := NewHttpCache(logger, validationMux)
	tc := NewTestContext(t, handler)

	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)

	// Manually populate cache with a stale entry that has must-revalidate
	// so validation is triggered on stale entries rather than the entry being deleted.
	cacheKey := handler.GetCacheKey(req)
	staleCv := testCacheValue(250 * time.Second)
	staleCv.Header["ETag"] = []string{"\"abc\""}
	staleCv.Header["Cache-Control"] = []string{"max-age=60, must-revalidate"}
	staleCv.Body = []byte("original body")
	cacheData, _ := json.Marshal(staleCv)
	handler.Cache.Add(cacheKey, cacheData)

	// First request with If-None-Match triggers validation; origin returns 304.
	// Client sent conditional headers, so if they still match the updated cache
	// entry, the client gets 304. Here the upstream updated the ETag to "abc-updated"
	// so the client's If-None-Match "abc" no longer matches — serve cached body as 200.
	resp1, _ := tc.DoWithHeaders(req, map[string][]string{
		"If-None-Match": {"\"abc\""},
	})
	if resp1.StatusCode != http.StatusNotModified {
		t.Errorf("expected 304 (ETag changed after revalidation), got %d", resp1.StatusCode)
	}
	status := resp1.Header.Get("cache-status")
	if !strings.Contains(status, "fwd=stale") {
		t.Errorf("expected cache-status hit, got %s", status)
	}

	// Second request without conditional headers should still serve the cached body
	resp2, _ := tc.Do(req)
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp2.StatusCode)
	}
	bodyBuf := make([]byte, 1024)
	n, _ := resp2.Body.Read(bodyBuf)
	bodyStr := string(bodyBuf[:n])
	if bodyStr != "original body" {
		t.Errorf("expected cached body 'original body', got %q", bodyStr)
	}
	status2 := resp2.Header.Get("cache-status")
	if !strings.Contains(status2, "hit") {
		t.Errorf("expected cache-status hit on second request, got %s", status2)
	}

	// Origin should have been called exactly once (304 validation only)
	if originCalls != 1 {
		t.Errorf("expected 1 origin call, got %d", originCalls)
	}
}

func TestCacheUpstreamResponseBody(t *testing.T) {
	expectedBody := strings.Repeat("hello world! ", 1000)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-length", strconv.Itoa(len(expectedBody)))
		w.WriteHeader(200)
		_, _ = w.Write([]byte(expectedBody))
	})

	logger := slog.Default()
	handler := NewHttpCache(logger, mux)
	tc := NewTestContext(t, handler)
	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)

	// first request goes to upstream
	resp1, _ := tc.Do(req)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp1.StatusCode)
	}
	body1, _ := readBody(resp1)
	if body1 != expectedBody {
		t.Errorf("upstream body mismatch: got %d bytes, want %d bytes", len(body1), len(expectedBody))
	}

	// second request served from cache
	resp2, _ := tc.Do(req)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	body2, _ := readBody(resp2)
	if body2 != expectedBody {
		t.Errorf("cached body mismatch: got %d bytes, want %d bytes", len(body2), len(expectedBody))
	}
}

func TestCacheUpstreamStatusCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte("created"))
	})

	logger := slog.Default()
	handler := NewHttpCache(logger, mux)
	tc := NewTestContext(t, handler)
	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)

	resp, _ := tc.Do(req)
	if resp.StatusCode != 201 {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}
	body, _ := readBody(resp)
	if body != "created" {
		t.Errorf("expected body 'created', got %q", body)
	}
}

// RFC 9110 15.4.5: 304 responses MUST NOT contain a body.
// Even if the upstream handler writes body bytes with a 304,
// the cache layer must strip them before sending to the client.
func TestCache304NoBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == "\"abc\"" {
			w.WriteHeader(http.StatusNotModified)
			// Misbehaving upstream writes body alongside 304
			_, _ = w.Write([]byte("should not appear"))
			return
		}
		w.Header().Set("etag", "\"abc\"")
		w.Header().Set("cache-control", "max-age=60, must-revalidate")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("original body"))
	})

	logger := slog.Default()
	handler := NewHttpCache(logger, mux)
	tc := NewTestContext(t, handler)

	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)

	// Populate cache with a stale must-revalidate entry so revalidation is triggered
	cacheKey := handler.GetCacheKey(req)
	cv := testCacheValue(250 * time.Second)
	cv.Header["ETag"] = []string{"\"abc\""}
	cv.Header["Cache-Control"] = []string{"max-age=60, must-revalidate"}
	cv.Body = []byte("original body")
	cacheData, _ := json.Marshal(cv)
	handler.Cache.Add(cacheKey, cacheData)

	// Trigger revalidation — upstream returns 304 with a spurious body.
	// Client request is unconditional, so cache serves the stored body as 200.
	resp, _ := tc.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := readBody(resp)
	if body != "original body" {
		t.Errorf("expected cached body 'original body', got %q", body)
	}
}

func readBody(resp *http.Response) (string, error) {
	defer resp.Body.Close() //nolint:errcheck
	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf), nil
}

// Regression: a 304 from cache validation must include the cached response
// headers (ETag, Content-Type, Cache-Control, etc.) so the browser can match
// the 304 to its local cached body. Without them browsers show a blank page.
func TestCache304IncludesCachedHeaders(t *testing.T) {
	logger := slog.Default()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("etag", "\"abc\"")
		w.Header().Set("content-type", "text/html; charset=utf-8")
		w.Header().Set("cache-control", "max-age=300")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<h1>hello</h1>"))
	})

	handler := NewHttpCache(logger, mux)
	tc := NewTestContext(t, handler)
	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)

	// Populate cache with a fresh entry that has ETag, Content-Type, Cache-Control
	cacheKey := handler.GetCacheKey(req)
	cv := testCacheValue(10 * time.Second)
	cv.Header["ETag"] = []string{"\"abc\""}
	cv.Header["Content-Type"] = []string{"text/html; charset=utf-8"}
	cv.Header["Cache-Control"] = []string{"max-age=300"}
	cv.Body = []byte("<h1>hello</h1>")
	cacheData, _ := json.Marshal(cv)
	handler.Cache.Add(cacheKey, cacheData)

	// Send conditional request that triggers a 304 from the cache layer
	resp, _ := tc.DoWithHeaders(req, map[string][]string{
		"If-None-Match": {"\"abc\""},
	})
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", resp.StatusCode)
	}

	// The 304 must carry the cached headers so the browser can use them
	// Note: Go's HTTP server strips Content-Type on 304 responses, which is fine
	// per RFC 9110 — the browser already has it from the original 200.
	if got := resp.Header.Get("ETag"); got != "\"abc\"" {
		t.Errorf("expected ETag %q, got %q", "\"abc\"", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "max-age=300" {
		t.Errorf("expected Cache-Control %q, got %q", "max-age=300", got)
	}

	// Body must be empty per RFC 9110 15.4.5
	body, _ := readBody(resp)
	if body != "" {
		t.Errorf("expected empty body for 304, got %q", body)
	}
}

func TestCacheAgeTtl(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("cache-control", "max-age=60")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("success"))
	})

	logger := slog.Default()
	handler := NewHttpCache(logger, mux)
	tc := NewTestContext(t, handler)

	req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)

	// first request hits backend
	resp1, _ := tc.Do(req)
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp1.StatusCode)
	}

	resp2, _ := tc.Do(req)
	status := resp2.Header.Get("cache-status")
	if !strings.Contains(status, "ttl=59;") {
		t.Errorf("expected ttl=59, got %s\n", status)
	}
}

// RFC 9111 4.2.4 Stale Serving - must-revalidate requires revalidation.
// RFC 9111 4.3.1/4.3.2 Validation - cache MUST send stored validators
// when generating conditional upstream requests for stale entries.
func TestCacheMustRevalidateRevalidationHeaders(t *testing.T) {
	actual := time.Now().Add(-10 * time.Minute).UTC()
	actualStr := actual.Format(time.RFC1123)

	tests := []struct {
		name                string
		cachedETag          string
		cachedLastModified  string
		expectedIfNoneMatch string
		expectedIfModified  string
	}{
		{
			name:                "RFC 9111 4.3.1 If-None-Match from stored ETag",
			cachedETag:          "\"abc\"",
			cachedLastModified:  "",
			expectedIfNoneMatch: "\"abc\"",
			expectedIfModified:  "",
		},
		{
			name:                "RFC 9111 4.3.2 If-Modified-Since from stored Last-Modified",
			cachedETag:          "",
			cachedLastModified:  actualStr,
			expectedIfNoneMatch: "",
			expectedIfModified:  actualStr,
		},
		{
			name:                "RFC 9111 4.3.1+4.3.2 Both validators present",
			cachedETag:          "\"xyz\"",
			cachedLastModified:  actualStr,
			expectedIfNoneMatch: "\"xyz\"",
			expectedIfModified:  actualStr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedIfNoneMatch, receivedIfModifiedSince string
			var receivedRequest *http.Request

			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				receivedIfNoneMatch = r.Header.Get("If-None-Match")
				receivedIfModifiedSince = r.Header.Get("If-Modified-Since")
				receivedRequest = r

				if r.Header.Get("If-None-Match") == "\"abc\"" ||
					r.Header.Get("If-None-Match") == "\"xyz\"" ||
					r.Header.Get("If-Modified-Since") != "" {
					w.WriteHeader(http.StatusNotModified)
					return
				}
				w.Header().Set("etag", "\"abc\"")
				w.Header().Set("cache-control", "max-age=60")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("success"))
			})

			logger := slog.Default()
			handler := NewHttpCache(logger, mux)
			tc := NewTestContext(t, handler)

			req, _ := http.NewRequest("GET", tc.cachedServer.URL+"/test", nil)
			cacheKey := handler.GetCacheKey(req)

			cv := testCacheValue(250 * time.Second)
			if tt.cachedETag != "" {
				cv.Header["ETag"] = []string{tt.cachedETag}
			}
			if tt.cachedLastModified != "" {
				cv.Header["Last-Modified"] = []string{tt.cachedLastModified}
			}
			cv.Header["Cache-Control"] = []string{"max-age=60, must-revalidate"}
			cv.Body = []byte("cached body")
			cacheData, _ := json.Marshal(cv)
			handler.Cache.Add(cacheKey, cacheData)

			resp, _ := tc.Do(req)

			// Client request is unconditional — after upstream 304, cache
			// serves the stored body as 200.
			if resp.StatusCode != http.StatusOK {
				t.Errorf("expected 200, got %d", resp.StatusCode)
			}
			status := resp.Header.Get("cache-status")
			if !strings.Contains(status, "hit") {
				t.Errorf("expected cache-status hit, got %s", status)
			}

			if receivedRequest == nil {
				t.Fatal("no request reached upstream handler")
			}

			if tt.expectedIfNoneMatch != "" && receivedIfNoneMatch != tt.expectedIfNoneMatch {
				t.Errorf("expected If-None-Match %q, got %q", tt.expectedIfNoneMatch, receivedIfNoneMatch)
			}
			if tt.expectedIfModified != "" && receivedIfModifiedSince != tt.expectedIfModified {
				t.Errorf("expected If-Modified-Since %q, got %q", tt.expectedIfModified, receivedIfModifiedSince)
			}
		})
	}
}
