package pgs

import (
	"slices"
	"testing"
	"time"
)

func TestSiteCachePurgeSite(t *testing.T) {
	evicted := []string{}
	c := NewSiteCache(0, time.Minute, func(key string, _ []byte) { evicted = append(evicted, key) })
	keys := []string{
		"antonio-blog__GET__/",
		"antonio-blog__GET__/about",
		"antonio-blog2__GET__/",
		"antonio-blog-v2__GET__/",
	}
	for _, key := range keys {
		c.Add(key, []byte("x"))
	}

	c.PurgeSite("antonio-blog")
	got := c.Keys()
	slices.Sort(got)
	want := []string{"antonio-blog-v2__GET__/", "antonio-blog2__GET__/"}
	if !slices.Equal(got, want) {
		t.Fatalf("after purge got %q, want %q", got, want)
	}
	if len(evicted) != 2 {
		t.Fatalf("eviction callback ran %d times, want 2", len(evicted))
	}

	// Entries that leave the cache on their own leave the index too.
	c.Remove("antonio-blog2__GET__/")
	if _, ok := c.sites["antonio-blog2"]; ok {
		t.Fatal("removed entry still indexed")
	}
	c.Purge()
	if len(c.sites) != 0 {
		t.Fatalf("index holds %d sites after a full purge", len(c.sites))
	}
}
