package http_cache

import (
	"net/http"
	"net/http/httptest"
	"testing"

)

type PicoCacheHandler struct {
	Upstream http.Handler
}

func (c *PicoCacheHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c.Upstream == nil {
		http.Error(w, "upstream http handler not found", http.StatusNotFound)
		return
	}
	c.Upstream.ServeHTTP(w, r)
}

func NewPicoCacheHandler(upstream http.Handler) *PicoCacheHandler {
	return &PicoCacheHandler{
		Upstream: upstream,
	}
}

// CacheMiddlewareFactory creates a cache middleware wrapper for testing.
// This abstraction allows swapping implementations (Souin → custom).
type CacheMiddlewareFactory interface {
	CreateHandler(next http.Handler) http.Handler
}

// TestContext holds shared test state.
type TestContext struct {
	t              *testing.T
	handler http.Handler
	cachedServer   *httptest.Server
}

// NewTestContext creates a test context with a backend and cached server.
func NewTestContext(t *testing.T, cacheHandler http.Handler) *TestContext {
	tc := &TestContext{
		t:              t,
		handler: cacheHandler,
	}

	tc.cachedServer = httptest.NewServer(tc.handler)
	t.Cleanup(tc.cachedServer.Close)

	return tc
}

func (tc *TestContext) Get(path string, headers ...string) (*http.Response, error) {
	req, _ := http.NewRequest("GET", tc.cachedServer.URL+path, nil)
	for i := 0; i < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return http.DefaultClient.Do(req)
}

func (tc *TestContext) GetHeader(resp *http.Response, key string) string {
	return resp.Header.Get(key)
}

func TestCacheHandler(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("success"))
	})

	handler := NewPicoCacheHandler(mux)
	tc := NewTestContext(t, handler)
	resp1, _ := tc.Get("/test")
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp1.StatusCode)
	}
}

// // RFC 9111 Section 5.1: Cache-Control: max-age.
// func TestMaxAgeDirective(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		w.Header().Set("Cache-Control", "max-age=5")
// 		w.Header().Set("Content-Type", "text/plain")
// 		_, _ = fmt.Fprintf(w, "request count: %d", requestCount)
// 	})

// 	// First request should hit backend
// 	resp1, _ := tc.Get("/test")
// 	if resp1.StatusCode != http.StatusOK {
// 		t.Errorf("expected 200, got %d", resp1.StatusCode)
// 	}

// 	// Second request within max-age should be cached
// 	resp2, _ := tc.Get("/test")
// 	if resp2.StatusCode != http.StatusOK {
// 		t.Errorf("expected 200, got %d", resp2.StatusCode)
// 	}

// 	// Verify request wasn't made to backend (same response indicates cache hit)
// 	// In a real scenario, you'd track backend hits via instrumentation
// 	if requestCount != 1 {
// 		t.Errorf("expected 1 backend request (cached), got %d", requestCount)
// 	}
// }

// // RFC 9111 Section 5.2.1: no-store directive.
// func TestNoStoreDirective(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		w.Header().Set("Cache-Control", "no-store")
// 		w.Header().Set("Content-Type", "text/plain")
// 		_, _ = fmt.Fprintf(w, "request count: %d", requestCount)
// 	})

// 	// Request with no-store should always hit backend
// 	_, _ = tc.Get("/test")
// 	_, _ = tc.Get("/test")
// 	_, _ = tc.Get("/test")

// 	if requestCount != 3 {
// 		t.Errorf("no-store should bypass cache: expected 3 backend requests, got %d", requestCount)
// 	}
// }

// // RFC 9111 Section 5.2.2: no-cache directive.
// // Must revalidate with origin server before using cached response.
// func TestNoCacheDirective(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		w.Header().Set("Cache-Control", "no-cache")
// 		w.Header().Set("ETag", fmt.Sprintf(`"%d"`, requestCount))
// 		w.Header().Set("Content-Type", "text/plain")
// 		_, _ = fmt.Fprintf(w, "request count: %d", requestCount)
// 	})

// 	// First request
// 	resp1, _ := tc.Get("/test")
// 	etag1 := tc.GetHeader(resp1, "ETag")

// 	// no-cache means must revalidate, so backend should be hit
// 	resp2, _ := tc.Get("/test")
// 	etag2 := tc.GetHeader(resp2, "ETag")

// 	if etag1 == etag2 && requestCount > 1 {
// 		t.Logf("no-cache directive: backend was recontacted as expected (request count: %d)", requestCount)
// 	}
// }

// // RFC 9111 Section 5.3: Expires header (absolute expiration time).
// func TestExpiresHeader(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		// Expires in the future (note: Cache-Control takes precedence)
// 		expireTime := time.Now().Add(5 * time.Second)
// 		w.Header().Set("Expires", expireTime.Format(http.TimeFormat))
// 		w.Header().Set("Cache-Control", "public, max-age=5")
// 		w.Header().Set("Content-Type", "text/plain")
// 		_, _ = fmt.Fprintf(w, "request count: %d", requestCount)
// 	})

// 	resp1, _ := tc.Get("/test")
// 	resp2, _ := tc.Get("/test")

// 	if resp1.StatusCode != http.StatusOK || resp2.StatusCode != http.StatusOK {
// 		t.Errorf("expected 200, got %d and %d", resp1.StatusCode, resp2.StatusCode)
// 	}

// 	if requestCount != 1 {
// 		t.Errorf("expected 1 backend request (Expires should cache), got %d", requestCount)
// 	}
// }

// // RFC 9111 Section 5.1.5: s-maxage (shared cache max-age).
// func TestSMaxAgeDirective(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		// s-maxage takes precedence for shared caches over max-age
// 		w.Header().Set("Cache-Control", "max-age=1, s-maxage=10")
// 		w.Header().Set("Content-Type", "text/plain")
// 		_, _ = fmt.Fprintf(w, "request count: %d", requestCount)
// 	})

// 	_, _ = tc.Get("/test")
// 	_, _ = tc.Get("/test")

// 	if requestCount != 1 {
// 		t.Errorf("s-maxage should be used by shared cache, expected 1 backend request, got %d", requestCount)
// 	}
// }

// // RFC 9111 Section 4.1: Vary header (cache key includes header values).
// func TestVaryHeader(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		acceptLang := r.Header.Get("Accept-Language")
// 		w.Header().Set("Cache-Control", "max-age=5")
// 		w.Header().Set("Vary", "Accept-Language")
// 		w.Header().Set("Content-Type", "text/plain")
// 		_, _ = fmt.Fprintf(w, "language: %s, count: %d", acceptLang, requestCount)
// 	})

// 	// Request with one Accept-Language
// 	resp1, _ := tc.Get("/test", "Accept-Language", "en")
// 	// Second request with same Accept-Language should hit cache
// 	resp2, _ := tc.Get("/test", "Accept-Language", "en")
// 	// Request with different Accept-Language should miss cache
// 	resp3, _ := tc.Get("/test", "Accept-Language", "fr")

// 	if resp1.StatusCode != http.StatusOK || resp2.StatusCode != http.StatusOK || resp3.StatusCode != http.StatusOK {
// 		t.Error("expected all requests to return 200")
// 	}

// 	if requestCount < 2 {
// 		t.Errorf("Vary header should create separate cache entries, expected >= 2 backend requests, got %d", requestCount)
// 	}
// }

// // RFC 9111 Section 4.2.3: ETag + If-None-Match (conditional request).
// func TestETagConditionalRequest(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		etag := `"abc123"`
// 		w.Header().Set("ETag", etag)
// 		w.Header().Set("Cache-Control", "no-cache")
// 		w.Header().Set("Content-Type", "text/plain")

// 		// If client sends If-None-Match and it matches, return 304
// 		if r.Header.Get("If-None-Match") == etag {
// 			w.WriteHeader(http.StatusNotModified)
// 			return
// 		}

// 		_, _ = fmt.Fprintf(w, "content v1")
// 	})

// 	resp1, _ := tc.Get("/test")
// 	if resp1.StatusCode != http.StatusOK {
// 		t.Errorf("first request should be 200, got %d", resp1.StatusCode)
// 	}

// 	// Second request may send If-None-Match header
// 	resp2, _ := tc.Get("/test")
// 	if resp2.StatusCode != http.StatusOK && resp2.StatusCode != http.StatusNotModified {
// 		t.Errorf("second request should be 200 or 304, got %d", resp2.StatusCode)
// 	}
// }

// // RFC 9111 Section 4.2.4: Last-Modified + If-Modified-Since.
// func TestLastModifiedConditionalRequest(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		lastMod := time.Now().Add(-1 * time.Hour).Format(http.TimeFormat)
// 		w.Header().Set("Last-Modified", lastMod)
// 		w.Header().Set("Cache-Control", "max-age=0, must-revalidate")
// 		w.Header().Set("Content-Type", "text/plain")

// 		// If client sends If-Modified-Since and it's after Last-Modified, return 304
// 		if ims := r.Header.Get("If-Modified-Since"); ims != "" {
// 			ifTime, _ := time.Parse(http.TimeFormat, ims)
// 			modTime, _ := time.Parse(http.TimeFormat, lastMod)
// 			if !ifTime.Before(modTime) {
// 				w.WriteHeader(http.StatusNotModified)
// 				return
// 			}
// 		}

// 		_, _ = fmt.Fprintf(w, "content, request %d", requestCount)
// 	})

// 	resp1, _ := tc.Get("/test")
// 	if resp1.StatusCode != http.StatusOK {
// 		t.Errorf("first request should be 200, got %d", resp1.StatusCode)
// 	}

// 	resp2, _ := tc.Get("/test")
// 	// Second request may trigger revalidation
// 	if resp2.StatusCode != http.StatusOK && resp2.StatusCode != http.StatusNotModified {
// 		t.Errorf("second request should be 200 or 304, got %d", resp2.StatusCode)
// 	}
// }

// // RFC 9111 Section 5.1.2: public vs private.
// func TestPublicVsPrivateDirective(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		// private means only private cache (browser) can store, not shared cache (proxy)
// 		// But Souin is a shared cache, so this should affect behavior
// 		w.Header().Set("Cache-Control", "private, max-age=10")
// 		w.Header().Set("Content-Type", "text/plain")
// 		_, _ = fmt.Fprintf(w, "request %d", requestCount)
// 	})

// 	_, _ = tc.Get("/test")
// 	_, _ = tc.Get("/test")

// 	// Behavior depends on cache implementation; document what your implementation does
// 	t.Logf("private directive test: request count %d (behavior depends on shared vs private cache)", requestCount)
// }

// // RFC 9111 Section 5.1.6: must-revalidate.
// func TestMustRevalidateDirective(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		w.Header().Set("Cache-Control", "max-age=0, must-revalidate")
// 		w.Header().Set("ETag", fmt.Sprintf(`"%d"`, requestCount))
// 		w.Header().Set("Content-Type", "text/plain")
// 		_, _ = fmt.Fprintf(w, "request %d", requestCount)
// 	})

// 	_, _ = tc.Get("/test")
// 	_, _ = tc.Get("/test")

// 	// must-revalidate means must check with origin server even if stale
// 	// Should see multiple backend requests
// 	t.Logf("must-revalidate test: request count %d", requestCount)
// }

// // RFC 9111 Section 5.1.7: proxy-revalidate.
// func TestProxyRevalidateDirective(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		w.Header().Set("Cache-Control", "max-age=0, proxy-revalidate")
// 		w.Header().Set("Content-Type", "text/plain")
// 		_, _ = fmt.Fprintf(w, "request %d", requestCount)
// 	})

// 	_, _ = tc.Get("/test")
// 	_, _ = tc.Get("/test")

// 	t.Logf("proxy-revalidate test: request count %d", requestCount)
// }

// // RFC 9111 Section 5.1.8: immutable.
// func TestImmutableDirective(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		w.Header().Set("Cache-Control", "max-age=31536000, immutable")
// 		w.Header().Set("Content-Type", "application/javascript")
// 		_, _ = fmt.Fprintf(w, "// cached resource")
// 	})

// 	for i := 0; i < 5; i++ {
// 		_, _ = tc.Get("/app.js")
// 	}

// 	if requestCount != 1 {
// 		t.Errorf("immutable should be cached indefinitely, expected 1 backend request, got %d", requestCount)
// 	}
// }

// // RFC 9111 Section 5.4: stale-while-revalidate (serve stale while revalidating).
// func TestStaleWhileRevalidateDirective(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		requestCount++
// 		w.Header().Set("Cache-Control", "max-age=1, stale-while-revalidate=10")
// 		w.Header().Set("Content-Type", "text/plain")
// 		_, _ = fmt.Fprintf(w, "request %d", requestCount)
// 	})

// 	_, _ = tc.Get("/test")

// 	// Wait for max-age to expire
// 	time.Sleep(2 * time.Second)

// 	// Second request within stale-while-revalidate window
// 	_, _ = tc.Get("/test")

// 	t.Logf("stale-while-revalidate test: request count %d", requestCount)
// }

// // RFC 9111 Section 5.4: stale-if-error (serve stale on error).
// func TestStaleIfErrorDirective(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)
// 	requestCount := 0
// 	shouldError := false

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		if shouldError {
// 			w.WriteHeader(http.StatusServiceUnavailable)
// 			return
// 		}
// 		requestCount++
// 		w.Header().Set("Cache-Control", "max-age=1, stale-if-error=10")
// 		w.Header().Set("Content-Type", "text/plain")
// 		_, _ = fmt.Fprintf(w, "request %d", requestCount)
// 	})

// 	_, _ = tc.Get("/test")

// 	// Wait for max-age to expire
// 	time.Sleep(2 * time.Second)

// 	// Simulate error on backend
// 	shouldError = true
// 	resp, _ := tc.Get("/test")

// 	// Should serve stale response instead of error
// 	t.Logf("stale-if-error test: got status %d (should serve stale on error)", resp.StatusCode)
// }

// // RFC 9111 Section 6.2.4: Age header (cache age calculation).
// func TestAgeHeaderCalculation(t *testing.T) {
// 	factory := NewSouinMiddlewareFactory(10 * time.Second)

// 	tc := NewTestContext(t, factory, func(w http.ResponseWriter, r *http.Request) {
// 		w.Header().Set("Cache-Control", "max-age=10")
// 		w.Header().Set("Date", time.Now().Format(http.TimeFormat))
// 		w.Header().Set("Content-Type", "text/plain")
// 		_, _ = fmt.Fprintf(w, "content")
// 	})

// 	resp1, _ := tc.Get("/test")
// 	age1, _ := strconv.Atoi(tc.GetHeader(resp1, "Age"))

// 	// Wait a bit
// 	time.Sleep(500 * time.Millisecond)

// 	resp2, _ := tc.Get("/test")
// 	age2, _ := strconv.Atoi(tc.GetHeader(resp2, "Age"))

// 	// Age should increase on subsequent cache hits
// 	t.Logf("Age header test: age1=%d, age2=%d (age should increase or be present on cached responses)", age1, age2)
// }
