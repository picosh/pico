package router

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetCustomDomainCachesMisses(t *testing.T) {
	records := map[string][]string{}
	var lookups int
	var fail error
	lookupTXT = func(name string) ([]string, error) {
		lookups++
		if fail != nil {
			return nil, fail
		}
		if r, ok := records[name]; ok {
			return r, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	t.Cleanup(func() {
		lookupTXT = net.LookupTXT
		txtCache.Purge()
		txtMissCache.Purge()
	})

	if got := GetCustomDomain("example.com", "pgs"); got != "" || lookups != 1 {
		t.Fatalf("got %q after %d lookups", got, lookups)
	}
	if got := GetCustomDomain("example.com", "pgs"); got != "" || lookups != 1 {
		t.Fatalf("missing record was looked up again: %q after %d lookups", got, lookups)
	}

	// The uncached lookup sees a record as soon as it exists.
	records["_pgs.example.com."] = []string{" antonio-blog "}
	if got, err := LookupCustomDomain("example.com", "pgs"); got != "antonio-blog" || err != nil {
		t.Fatalf("LookupCustomDomain = %q, %v", got, err)
	}

	// Failures other than a missing record are not cached.
	fail = errors.New("i/o timeout")
	if got := GetCustomDomain("other.com", "pgs"); got != "" {
		t.Fatalf("got %q", got)
	}
	fail = nil
	records["_pgs.other.com."] = []string{"antonio-other"}
	if got := GetCustomDomain("other.com", "pgs"); got != "antonio-other" {
		t.Fatalf("lookup after a transient failure got %q", got)
	}
}

func TestNewHostRouter(t *testing.T) {
	serve := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(name + ":" + GetSubdomain(r)))
		})
	}
	get := func(h http.Handler, url string) string {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, url, nil))
		return res.Body.String()
	}

	h := NewHostRouter("pico.test", "prose", serve("main"), serve("sites"))
	if got := get(h, "https://pico.test/"); got != "main:" {
		t.Errorf("main domain = %q", got)
	}
	if got := get(h, "https://tester.pico.test/"); got != "sites:tester" {
		t.Errorf("subdomain = %q", got)
	}
	if got := get(NewHostRouter("pico.test", "prose", serve("main"), nil), "https://tester.pico.test/"); got != "main:" {
		t.Errorf("subdomain without sites = %q", got)
	}
}

func TestCreatePProfRoutesMux(t *testing.T) {
	mux := http.NewServeMux()
	CreatePProfRoutesMux(mux)
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap", "/debug/pprof/cmdline"} {
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK {
			t.Errorf("%s = %d", path, res.Code)
		}
	}
}
