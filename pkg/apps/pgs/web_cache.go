package pgs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils/pipe"
)

type PicoPubsub interface {
	io.Reader
	io.Writer
	io.Closer
}

type PubsubPipe struct {
	Pipe *pipe.ReconnectReadWriteCloser
}

func (p *PubsubPipe) Read(b []byte) (int, error) {
	return p.Pipe.Read(b)
}

func (p *PubsubPipe) Write(b []byte) (int, error) {
	return p.Pipe.Write(b)
}

func (p *PubsubPipe) Close() error {
	return p.Pipe.Close()
}

func NewPubsubPipe(pipe *pipe.ReconnectReadWriteCloser) *PubsubPipe {
	return &PubsubPipe{
		Pipe: pipe,
	}
}

type PubsubChan struct {
	Chan chan []byte
}

func (p *PubsubChan) Read(b []byte) (int, error) {
	n := copy(b, <-p.Chan)
	return n, nil
}

func (p *PubsubChan) Write(b []byte) (int, error) {
	p.Chan <- b
	return len(b), nil
}

func (p *PubsubChan) Close() error {
	close(p.Chan)
	return nil
}

func NewPubsubChan() *PubsubChan {
	return &PubsubChan{
		Chan: make(chan []byte),
	}
}

func getSurrogateKey(userName, projectName string) string {
	return fmt.Sprintf("%s-%s", userName, projectName)
}

func CreatePubCacheDrain(ctx context.Context, logger *slog.Logger) *pipe.ReconnectReadWriteCloser {
	info := shared.NewPicoPipeClient()
	send := pipe.NewReconnectReadWriteCloser(
		ctx,
		logger,
		info,
		"pub to cache-drain",
		"pub cache-drain -b=false",
		100,
		-1,
	)
	return send
}

func CreateSubCacheDrain(ctx context.Context, logger *slog.Logger) *pipe.ReconnectReadWriteCloser {
	info := shared.NewPicoPipeClient()
	send := pipe.NewReconnectReadWriteCloser(
		ctx,
		logger,
		info,
		"sub to cache-drain",
		"sub cache-drain -k",
		100,
		-1,
	)
	return send
}

// purgeCache send a pipe pub to the pgs web instance which purges
// cached entries for a given subdomain (like "fakeuser-www-proj"). We set a
// "surrogate-key: <subdomain>" header on every pgs response which ensures all
// cached assets for a given subdomain are grouped under a single key (which is
// separate from the "GET-https-example.com-/path" key used for serving files
// from the cache).
func purgeCache(cfg *PgsConfig, writer io.Writer, surrogate string) error {
	cfg.Logger.Info("purging cache", "surrogate", surrogate)
	_, err := writer.Write([]byte(surrogate + "\n"))
	return err
}

func purgeAllCache(cfg *PgsConfig, writer io.Writer) error {
	return purgeCache(cfg, writer, "*")
}

// SiteCache holds the http cache's responses and indexes their keys by site,
// so purging one site doesn't scan the whole cache.
type SiteCache struct {
	*expirable.LRU[string, []byte]
	mu    sync.Mutex
	sites map[string]map[string]struct{}
}

func NewSiteCache(size int, ttl time.Duration, onEvict func(key string, value []byte)) *SiteCache {
	c := &SiteCache{sites: map[string]map[string]struct{}{}}
	c.LRU = expirable.NewLRU(size, func(key string, value []byte) {
		c.unindex(key)
		if onEvict != nil {
			onEvict(key, value)
		}
	}, ttl)
	return c
}

// cacheKeySite returns the site a PgsCacheKey belongs to.
func cacheKeySite(key string) string {
	site, _, _ := strings.Cut(key, "__")
	return site
}

func (c *SiteCache) Add(key string, value []byte) bool {
	evicted := c.LRU.Add(key, value)
	site := cacheKeySite(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sites[site] == nil {
		c.sites[site] = map[string]struct{}{}
	}
	c.sites[site][key] = struct{}{}
	return evicted
}

// PurgeSite removes every cached response for a site. The index lock is
// released before touching the LRU, whose eviction callback takes it.
func (c *SiteCache) PurgeSite(site string) {
	c.mu.Lock()
	keys := c.sites[site]
	delete(c.sites, site)
	c.mu.Unlock()
	for key := range keys {
		c.Remove(key)
	}
}

func (c *SiteCache) unindex(key string) {
	site := cacheKeySite(key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if keys, ok := c.sites[site]; ok {
		delete(keys, key)
		if len(keys) == 0 {
			delete(c.sites, site)
		}
	}
}
