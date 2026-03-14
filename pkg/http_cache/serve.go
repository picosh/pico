package http_cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
)

type PicoCacheHandler struct {
	Ttl      time.Duration
	Upstream http.Handler
	Cache    Cacher
	Logger   *slog.Logger
}

func getCacheKey(r *http.Request) string {
	return r.Method + "__" + r.Host + "__" + r.URL.RequestURI()
}

func serveCache(w http.ResponseWriter, ttl float64, cacheKey string, cacheValue *CacheValue) {
	hdr := w.Header()
	for key, values := range cacheValue.Header {
		for _, value := range values {
			hdr.Add(key, value)
		}
	}

	age := calcAge(cacheValue.CreatedAt)
	hdr.Add("age", strconv.Itoa(int(age)))

	hdr.Add("cache-status", cacheStatusHit(cacheKey, ttl))
	w.Write(cacheValue.Body)
	w.WriteHeader(http.StatusOK)
}

func isCachable(r *http.Request) error {
	return nil
}

var ErrCacheStale = errors.New("cache is stale")

func isCacheValid(r *http.Request) error {
	control := r.Header.Get("cache-control")
	// RFC 9111 Request Cache-Control no-cache
	// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.4
	if strings.Contains(control, "no-cache") {
		return fmt.Errorf("detected no-cache")
	}

	return nil
}

func (c *PicoCacheHandler) maybeUseCache(cacheKey string, w http.ResponseWriter, r *http.Request) error {
	data, exists := c.Cache.Get(cacheKey)
	if !exists {
		return fmt.Errorf("no cache stored")
	}

	var cacheValue CacheValue
	err := json.Unmarshal(data, &cacheValue)
	if err != nil {
		return fmt.Errorf("json unmarshal: %w", err)
	}

	isValid := isCacheValid(r)
	if isValid != nil {
		if errors.Is(err, ErrCacheStale) {
			fmt.Errorf("removing cache")
			c.Cache.Remove(cacheKey)
		}
		return fmt.Errorf("cache invalid: %w", isValid)
	}

	ttl := calcFreshness(c.Ttl, cacheValue.CreatedAt)
	serveCache(w, ttl.Seconds(), cacheKey, &cacheValue)
	return nil
}

// RFC 9111 4.2.1 Calculating Freshness
// https://www.rfc-editor.org/rfc/rfc9111#section-4.2.1
func calcFreshness(ttl time.Duration, createdAt time.Time) time.Duration {
	age := calcAge(createdAt)
	return ttl - age
}

// RFC 9111 4.2.3 Calculating Age
// https://www.rfc-editor.org/rfc/rfc9111.html#section-4.2.3
func calcAge(createdAt time.Time) time.Duration {
	return time.Since(createdAt)
}

func cacheStatusHit(cacheKey string, ttl float64) string {
	// RFC 9211 2.1 Cache-Status hit
	// https://www.rfc-editor.org/rfc/rfc9211#section-2.1
	// RFC 9211 2.4 Cache-status ttl
	// https://www.rfc-editor.org/rfc/rfc9211#section-2.4
	// RFC 9222 2.7 Cache-status key
	// https://www.rfc-editor.org/rfc/rfc9211#section-2.7
	return fmt.Sprintf("pico; hit; ttl=%d; key=%s", int(ttl), cacheKey)
}

func cacheStatusMiss(cacheKey string, ttl float64, stored bool) string {
	// RFC 9211 2.2 Cache-Status fwd
	// https://www.rfc-editor.org/rfc/rfc9211#section-2.2
	status := "pico; fwd=uri-miss"
	if stored {
		// RFC 9211 2.2 Cache-Status stored
		// https://www.rfc-editor.org/rfc/rfc9211#section-2.5
		// RFC 9211 2.4 Cache-status ttl
		// https://www.rfc-editor.org/rfc/rfc9211#section-2.4
		status = fmt.Sprintf("%s; ttl=%d; stored", status, int(ttl))
	}
	// RFC 9222 2.7 Cache-status key
	// https://www.rfc-editor.org/rfc/rfc9211#section-2.7
	status = fmt.Sprintf("%s; key=%s", status, cacheKey)
	return status
}

func (c *PicoCacheHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c.Upstream == nil {
		http.Error(w, "upstream http handler not found", http.StatusNotFound)
		return
	}
	cacheKey := getCacheKey(r)
	log := c.Logger.With("cache_key", cacheKey)

	err := c.maybeUseCache(cacheKey, w, r)
	if err == nil {
		log.Info("cache hit")
		return
	}

	log.Info("cache miss, requesting upstream", "err", err)
	wrapped := &responseWriter{ResponseWriter: w}
	c.Upstream.ServeHTTP(wrapped, r)

	err = isCachable(r)
	if err == nil {
		log.Info("storing cache")
		nextValue := wrapped.ToCacheValue()
		enc, _ := json.Marshal(nextValue)
		c.Cache.Add(cacheKey, enc)
		wrapped.Header().Set("cache-status", cacheStatusMiss(cacheKey, c.Ttl.Seconds(), true))
	} else {
		log.Info("not cachable", "err", err)
		wrapped.Header().Set("cache-status", cacheStatusMiss(cacheKey, 0, false))
	}

	total, err := wrapped.ResponseWriter.Write(wrapped.Body())
	log.Info("response writer", "bytes_written", total)
	if err != nil {
		log.Error("response writer write", "err", err)
	}
	wrapped.Send()
}

func NewPicoCacheHandler(log *slog.Logger, upstream http.Handler) *PicoCacheHandler {
	ttl := time.Minute * 10
	cache := expirable.NewLRU[string, []byte](0, nil, ttl)
	return &PicoCacheHandler{
		Ttl:      ttl,
		Logger:   log,
		Upstream: upstream,
		Cache:    cache,
	}
}
