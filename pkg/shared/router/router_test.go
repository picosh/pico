package router

import (
	"errors"
	"net"
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
	records["_pgs.example.com"] = []string{" antonio-blog "}
	if got, err := LookupCustomDomain("example.com", "pgs"); got != "antonio-blog" || err != nil {
		t.Fatalf("LookupCustomDomain = %q, %v", got, err)
	}

	// Failures other than a missing record are not cached.
	fail = errors.New("i/o timeout")
	if got := GetCustomDomain("other.com", "pgs"); got != "" {
		t.Fatalf("got %q", got)
	}
	fail = nil
	records["_pgs.other.com"] = []string{"antonio-other"}
	if got := GetCustomDomain("other.com", "pgs"); got != "antonio-other" {
		t.Fatalf("lookup after a transient failure got %q", got)
	}
}
