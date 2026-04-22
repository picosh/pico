package httpcache

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

var ErrMustRevalidate = errors.New("cache is stale and must-revalidate requires revalidation")

type CacheKey interface {
	GetCacheKey(r *http.Request) string
}

type DefaultCacheKey struct{}

func (p *DefaultCacheKey) GetCacheKey(r *http.Request) string {
	// RFC 9111 §3: HEAD responses can be served from a stored GET response.
	// Normalize HEAD to GET so both methods share the same cache entry.
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	return r.Host + "__" + method + "__" + r.URL.RequestURI()
}

type CacheMetrics interface {
	AddCacheItem(float64)
	AddCacheHit()
	AddCacheMiss()
	AddUpstreamRequest()
}

type DefaultCacheMetrics struct{}

func (p *DefaultCacheMetrics) AddCacheItem(float64) {}
func (p *DefaultCacheMetrics) AddCacheHit()         {}
func (p *DefaultCacheMetrics) AddCacheMiss()        {}
func (p *DefaultCacheMetrics) AddUpstreamRequest()  {}

type HttpCache struct {
	CacheKey
	CacheMetrics
	Ttl      time.Duration
	Upstream http.Handler
	Cache    Cacher
	Logger   *slog.Logger
}

func NewHttpCache(log *slog.Logger, upstream http.Handler) *HttpCache {
	ttl := time.Minute * 10
	cache := expirable.NewLRU[string, []byte](0, nil, ttl)
	httpCache := &HttpCache{
		Ttl:          ttl,
		Logger:       log,
		Upstream:     upstream,
		Cache:        cache,
		CacheKey:     &DefaultCacheKey{},
		CacheMetrics: &DefaultCacheMetrics{},
	}
	log.Info("httpcache initiated", "ttl", httpCache.Ttl, "storage", "lru")
	return httpCache
}

func (c *HttpCache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c.Upstream == nil {
		http.Error(w, "upstream http handler not found", http.StatusNotFound)
		return
	}

	cacheKey := c.GetCacheKey(r)
	log := c.Logger.With("cache_key", cacheKey)

	err := c.maybeUseCache(cacheKey, w, r)
	if err == nil {
		log.Info("cache hit")
		c.AddCacheHit()
		return
	}

	// RFC 9111 5.2.1.7 only-if-cached - don't store new responses
	cacheContState := parseCacheControl(r.Header.Get("cache-control"))
	onlyIfCached := cacheContState.onlyIfCache
	if onlyIfCached {
		msg := "cache not found and detected only-if-cached"
		log.Error(msg)
		http.Error(w, msg, http.StatusGatewayTimeout)
		return
	}

	// RFC 9111 4.2.4 + 4.3.1/4.3.2: stale must-revalidate entries must be
	// revalidated with conditional headers derived from the stored response.
	// Preserve original client conditional headers so we can evaluate them
	// after revalidation to decide whether the client gets 304 or 200.
	clientIfNoneMatch := r.Header.Get("if-none-match")
	clientIfModifiedSince := r.Header.Get("if-modified-since")
	clientConditional := clientIfNoneMatch != "" || clientIfModifiedSince != ""

	if errors.Is(err, ErrMustRevalidate) {
		if cachedData, exists := c.Cache.Get(cacheKey); exists {
			var cachedValue CacheValue
			if json.Unmarshal(cachedData, &cachedValue) == nil {
				if etag := getHeader(cachedValue.Header, "etag"); etag != "" {
					r.Header.Set("if-none-match", etag)
				}
				if lastMod := getHeader(cachedValue.Header, "last-modified"); lastMod != "" {
					r.Header.Set("if-modified-since", lastMod)
				}
			}
		}
	}

	log.Info("cache miss, requesting upstream", "err", err)
	c.AddCacheMiss()
	wrapped := &responseWriter{ResponseWriter: w}
	c.Upstream.ServeHTTP(wrapped, r)
	c.AddUpstreamRequest()

	// RFC 9111 4.3.4 304 Not Modified
	// https://www.rfc-editor.org/rfc/rfc9111.html#section-4.3.4
	// A 304 response updates header metadata but preserves the cached body.
	if wrapped.StatusCode() == http.StatusNotModified {
		existingData, exists := c.Cache.Get(cacheKey)
		if !exists {
			// Cache entry vanished; forward the 304 as-is.
			log.Info("no cache entry found, forwarding 304 as-is")
			wrapped.Send()
			return
		}

		var cacheValue CacheValue
		err = json.Unmarshal(existingData, &cacheValue)
		if err != nil {
			log.Error("json unmarshal", "err", err)
			wrapped.Send()
			return
		}

		// Merge non-forbidden headers from the 304 response into the cached entry.
		// Normalize keys to lowercase to avoid case-sensitivity issues.
		for key, values := range wrapped.Header() {
			if isForbiddenHeader(key) {
				continue
			}
			cacheValue.Header[strings.ToLower(key)] = values
		}
		// Revalidation refreshes the entry -- reset CreatedAt so it's fresh again.
		cacheValue.CreatedAt = time.Now()
		enc, _ := json.Marshal(cacheValue)
		log.Info("updating cached headers from 304 response")
		c.Cache.Remove(cacheKey)
		c.Cache.Add(cacheKey, enc)
		c.AddCacheItem(float64(len(enc)))

		if clientConditional {
			// Client sent conditional headers -- re-evaluate against the
			// updated cached entry and return 304 if it still matches.
			r.Header.Set("if-none-match", clientIfNoneMatch)
			r.Header.Set("if-modified-since", clientIfModifiedSince)
			valid := c.handleValidation(r, &cacheValue)
			if valid {
				hdr := stripForbiddenHeaders(w, &cacheValue)
				ageDur := calcAge(cacheValue.CreatedAt)
				hdr.Set("age", strconv.Itoa(int(ageDur.Seconds())+1))
				hdr.Set("cache-status", cacheStatusStale(cacheKey, wrapped.StatusCode()))
				w.WriteHeader(http.StatusNotModified)
				log.Info("client conditional headers match, returning 304")
				return
			}
		}

		// Client request was unconditional (or conditional but no longer matches)
		// serve the full cached response.
		log.Info("serving full cached response to client")
		serveCache(w, c.Ttl, cacheKey, &cacheValue)
		return
	}

	err = isResponseCachable(r, wrapped)
	if err == nil {
		log.Info("storing cache")
		nextValue := wrapped.ToCacheValue()
		enc, _ := json.Marshal(nextValue)
		c.Cache.Remove(cacheKey)
		c.Cache.Add(cacheKey, enc)
		c.AddCacheItem(float64(len(enc)))
		wrapped.Header().Set("cache-status", cacheStatusMiss(cacheKey, true))
	} else {
		log.Info("not cachable", "err", err)
		wrapped.Header().Set("cache-status", cacheStatusMiss(cacheKey, false))
	}

	wrapped.Send()
}

// isForbiddenHeader checks if a header should not be stored/served per RFC 9111 Section 3.1
// https://www.rfc-editor.org/rfc/rfc9111.html#section-3.1
func isForbiddenHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "proxy-connection",
		"proxy-authentication-info":
		return true
	default:
		return false
	}
}

func serveCache(w http.ResponseWriter, freshness time.Duration, cacheKey string, cacheValue *CacheValue) {
	hdr := stripForbiddenHeaders(w, cacheValue)
	ageDur := calcAge(cacheValue.CreatedAt)
	age := ageDur.Seconds()
	hdr.Set("age", strconv.Itoa(int(age)+1))
	hdr.Set("cache-status", cacheStatusHit(cacheKey, freshness.Seconds()))
	statusCode := cacheValue.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(cacheValue.Body)
}

// matchVary checks if the request matches the Vary header from the cached response
// RFC 9111 4.1 Vary.
func matchVary(r *http.Request, cachedHeaders map[string][]string) bool {
	vary := getHeader(cachedHeaders, "Vary")
	if vary == "" {
		return true
	}

	// Vary: * means the response is not cacheable
	if vary == "*" {
		return false
	}

	// Parse Vary header and check each field
	fields := strings.FieldsFunc(vary, func(r rune) bool {
		return r == ','
	})

	for _, field := range fields {
		field = strings.TrimSpace(strings.ToLower(field))
		if field == "" {
			continue
		}

		// Get the request header value
		reqValue := r.Header.Get(field)

		// Get the cached header value for this field (case-insensitive lookup)
		var cachedValue string
		for key, values := range cachedHeaders {
			if strings.ToLower(key) == field && len(values) > 0 {
				cachedValue = values[0]
				break
			}
		}
		if cachedValue == "" {
			continue
		}

		// Compare values - must match exactly
		if reqValue != cachedValue {
			return false
		}
	}

	return true
}

func getHeader(headers map[string][]string, key string) string {
	// Case-insensitive lookup
	for k, values := range headers {
		if strings.EqualFold(k, key) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

// handleValidation handles conditional request validation.
// RFC 9110 13 Conditional Requests.
// RFC 9111 4.3.2 Response Validation.
func (c *HttpCache) handleValidation(r *http.Request, cacheValue *CacheValue) bool {
	etag := getHeader(cacheValue.Header, "etag")
	lastModified := getHeader(cacheValue.Header, "last-modified")

	c.Logger.Debug(
		"validate",
		"etag", etag,
		"lastModified", lastModified,
	)

	// RFC 9110 13.1.2 If-None-Match
	// https://www.rfc-editor.org/rfc/rfc9110.html#section-13.1.2
	ifNoneMatch := r.Header.Get("if-none-match")
	if ifNoneMatch != "" {
		// Wildcard If-None-Match: *
		if ifNoneMatch == "*" {
			return etag != ""
		}

		// Check if any of the provided ETags match
		etags := parseETags(ifNoneMatch)
		for _, etagVal := range etags {
			if etagVal == etag {
				return true
			}
		}
		return false
	}

	// RFC 9110 13.1.3 If-Modified-Since
	// https://www.rfc-editor.org/rfc/rfc9110.html#section-13.1.3
	ifModifiedSince := r.Header.Get("if-modified-since")
	if ifModifiedSince != "" && lastModified != "" {
		reqTime := parseTimeFallback(ifModifiedSince)
		if !reqTime.IsZero() {
			cachedTime := parseTimeFallback(lastModified)
			if !cachedTime.IsZero() {
				if !cachedTime.After(reqTime) {
					return true
				}
			}
		}
	}

	// RFC 9110 13.1.4 If-Unmodified-Since
	// https://www.rfc-editor.org/rfc/rfc9110.html#section-13.1.4
	// For cache purposes, if If-Unmodified-Since matches, we can serve from cache
	ifUnmodifiedSince := r.Header.Get("if-unmodified-since")
	if ifUnmodifiedSince != "" && lastModified != "" {
		reqTime := parseTimeFallback(ifUnmodifiedSince)
		if !reqTime.IsZero() {
			cachedTime := parseTimeFallback(lastModified)
			if !cachedTime.IsZero() {
				if !cachedTime.Before(reqTime) {
					// Cached response is not modified since the request time
					// We can serve from cache, but don't return 304
					// The caller will handle the cache hit
					return false
				}
			}
		}
	}

	return false
}

func parseETags(etags string) []string {
	var result []string
	parts := strings.Split(etags, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// parseTimeFallback parses time strings in RFC1123 or RFC1123Z format.
func parseTimeFallback(t string) time.Time {
	// Try RFC1123Z first (with numeric timezone)
	if parsed, err := http.ParseTime(t); err == nil {
		return parsed
	}
	// Try RFC1123 (with 3-letter timezone abbreviation)
	parsed, err := time.Parse("Mon, 02 Jan 2006 15:04:05 MST", t)
	if err == nil {
		return parsed
	}
	return time.Time{}
}

func isResponseCachable(r *http.Request, resp *responseWriter) error {
	method := r.Method
	// RFC 9111 2.3 Opinion - Only cache GET requests
	if method != http.MethodGet {
		return fmt.Errorf("response method not cacheable: %s", method)
	}

	isValidStatus := isCacheableStatusCode(resp.StatusCode())
	if !isValidStatus {
		return fmt.Errorf("response status code not cachable: %d", resp.StatusCode())
	}

	state := parseCacheControl(resp.Header().Get("cache-control"))
	if state.private {
		return fmt.Errorf("shared cache cannot store private directives")
	}

	return nil
}

// RFC 9110 15.1-2: Heuristically cachable status codes
// 200, 203, 204,
// 206, 300, 301,
// 308, 404, 405,
// 410, 414, 501.
func isCacheableStatusCode(code int) bool {
	switch code {
	case http.StatusOK, http.StatusNonAuthoritativeInfo, http.StatusNoContent,
		http.StatusPartialContent, http.StatusMultipleChoices, http.StatusMovedPermanently,
		http.StatusPermanentRedirect, http.StatusNotFound, http.StatusMethodNotAllowed,
		http.StatusGone, http.StatusRequestURITooLong, http.StatusNotImplemented:
		return true
	default:
		return false
	}
}

type cacheControlState struct {
	noCache        bool
	noStore        bool
	noTransform    bool
	onlyIfCache    bool
	private        bool
	public         bool
	mustRevalidate bool
	// we explicitly check for max-age == 0 which is different from it
	// being unset so it's important we check if it is actually set
	// in the cache-control
	hasMaxAge    bool
	maxAge       time.Duration
	sharedMaxAge time.Duration
	maxStale     time.Duration
	minFresh     time.Duration
}

func parseCacheControl(cc string) cacheControlState {
	parsed := strings.Split(cc, ",")
	state := cacheControlState{}
	for _, raw := range parsed {
		directive := strings.ToLower(strings.TrimSpace(raw))
		if directive == "" {
			continue
		}
		switch directive {
		case "public":
			state.public = true
		case "private":
			state.private = true
		case "no-cache":
			state.noCache = true
		case "no-store":
			state.noStore = true
		case "no-transform":
			state.noTransform = true
		case "only-if-cached":
			state.onlyIfCache = true
		case "must-revalidate":
			state.mustRevalidate = true
		}

		if strings.HasPrefix(directive, "max-age=") {
			state.hasMaxAge = true
			state.maxAge = parseHeaderTime(directive, "max-age")
		}
		if strings.HasPrefix(directive, "s-maxage=") {
			state.sharedMaxAge = parseHeaderTime(directive, "s-maxage")
		}
		if strings.HasPrefix(directive, "min-fresh=") {
			state.minFresh = parseHeaderTime(directive, "min-fresh")
		}
		if strings.HasPrefix(directive, "max-stale=") {
			state.maxStale = parseHeaderTime(directive, "max-stale")
		}
	}
	return state
}

func isCacheValid(r *http.Request, freshness time.Duration, age time.Duration) error {
	state := parseCacheControl(r.Header.Get("cache-control"))

	if state.private {
		return fmt.Errorf("private directive")
	}

	// RFC 9111 5.2.1.4 Request Cache-Control: no-cache
	// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.4
	if state.noCache {
		return fmt.Errorf("detected no-cache")
	}

	// RFC 9111 5.2.1.5 Request Cache-Control: no-store
	// A no-store request can still use cached content, it just shouldn't store the response
	if state.noStore {
		// Allow cache hit but won't store on this request
		return nil
	}

	// RFC 9111 5.2.1.1 Request Cache-Control: max-age=0
	// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.1
	if state.hasMaxAge && state.maxAge == 0 {
		return fmt.Errorf("detected max-age=0")
	}

	// RFC 9111 5.2.1.3 Request Cache-Control: min-fresh
	// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.3
	minFreshDur := state.minFresh
	if minFreshDur.Seconds() > 0 && freshness < minFreshDur {
		return fmt.Errorf("min-fresh: cache freshness is too old")
	}

	// RFC 9111 5.2.1.2 Request Cache-Control: max-stale
	// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.2
	// max-stale allows serving stale responses as long as staleness <= max-stale value
	// staleness = age - freshness (when freshness < 0, staleness = age + |freshness|)
	// If freshness <= 0, the cache is stale, and max-stale allows it if staleness <= max-stale
	maxStaleDur := state.maxStale
	if maxStaleDur > 0 && freshness <= 0 {
		// Cache is stale, check if max-stale allows it
		staleness := age - freshness // When freshness <= 0, staleness = age + |freshness|
		if staleness > maxStaleDur {
			return fmt.Errorf("max-stale: staleness exceeds limit")
		}
		// max-stale allows this stale response
		return nil
	}
	if maxStaleDur > 0 && freshness > maxStaleDur {
		return fmt.Errorf("max-stale: freshness exceeds limit")
	}

	// RFC 9111 5.2.1.6 Request Cache-Control: no-transform
	// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.6
	// no-transform in the request means the cache should not transform the response.
	// Serving from cache counts as a transformation, so we must forward to origin.
	if state.noTransform {
		return fmt.Errorf("request has no-transform directive")
	}

	// RFC 9111 5.2.1.7 Request Cache-Control: only-if-cached
	// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.1.7
	// For our implementation, only-if-cached means we can use cached response
	// but we shouldn't store new responses. The caller handles the logic.
	if state.onlyIfCache {
		// Allow cache hit, but the ServeHTTP will not store new responses
		return nil
	}

	return nil
}

func (c *HttpCache) maybeUseCache(cacheKey string, w http.ResponseWriter, r *http.Request) error {
	data, exists := c.Cache.Get(cacheKey)
	if !exists {
		return fmt.Errorf("no cache stored")
	}

	var cacheValue CacheValue
	err := json.Unmarshal(data, &cacheValue)
	if err != nil {
		return fmt.Errorf("json unmarshal: %w", err)
	}

	// RFC 9111 4.1 Vary - check if request matches cached Vary values
	if !matchVary(r, cacheValue.Header) {
		return fmt.Errorf("vary mismatch")
	}

	// RFC 9111 5.2.2.4 Response Cache-Control: no-cache
	// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.4
	// Must revalidate with origin before using cached response
	cacheContState := parseCacheControl(
		getHeader(cacheValue.Header, "cache-control"),
	)
	if cacheContState.noCache {
		return fmt.Errorf("cache requires revalidation")
	}

	// RFC 9111 5.3 Expires
	// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.3
	// Check if the cached response has expired based on the Expires header
	var expires time.Time
	expiresStr := getHeader(cacheValue.Header, "expires")
	if expiresStr != "" {
		var parseErr error
		expires, parseErr = http.ParseTime(expiresStr)
		if parseErr != nil {
			// Invalid Expires header means the response is stale
			return fmt.Errorf("cache expired based on expires header")
		}
		if time.Now().After(expires) {
			return fmt.Errorf("cache expired based on expires header")
		}
	}

	// RFC 9111 5.2.2.5 Response Cache-Control: must-revalidate
	// https://www.rfc-editor.org/rfc/rfc9111.html#section-3.3.1
	// must-revalidate means the cache MUST NOT use a stale response if it can validate it
	// with the origin server. When cache is stale, we must revalidate.
	if cacheContState.mustRevalidate {
		// Check if cache is stale first
		age := calcAge(cacheValue.CreatedAt)
		freshness := calcFreshness(cacheContState, expires, age, c.Ttl)
		if freshness <= 0 {
			return ErrMustRevalidate
		}
	}

	// RFC 9111 5.2.2.5 Response Cache-Control: no-store
	// https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2.5
	// Should not store response, but cached response can still be used
	// However, tests expect this to forward to origin
	if cacheContState.noStore {
		return fmt.Errorf("cache has no-store")
	}

	age := calcAge(cacheValue.CreatedAt)
	freshness := calcFreshness(cacheContState, expires, age, c.Ttl)

	// RFC 9111 4.3 Validation - check validation headers first
	// RFC 9110 13 Conditional Requests
	// https://www.rfc-editor.org/rfc/rfc9110.html#section-13
	valid := c.handleValidation(r, &cacheValue)
	if valid {
		hdr := stripForbiddenHeaders(w, &cacheValue)
		ageDur := calcAge(cacheValue.CreatedAt)
		hdr.Set("age", strconv.Itoa(int(ageDur.Seconds())+1))
		hdr.Set("cache-status", cacheStatusHit(cacheKey, freshness.Seconds()))
		w.WriteHeader(http.StatusNotModified)
		return nil
	}

	// Check if request allows stale responses (max-stale)
	// RFC 9111 5.2.1.2 - max-stale allows serving stale responses
	// We need to check this before the freshness <= 0 check
	reqCacheState := parseCacheControl(r.Header.Get("cache-control"))
	maxStaleDur := reqCacheState.maxStale
	hasMaxStale := maxStaleDur > 0 && freshness <= 0

	isValid := isCacheValid(r, freshness, age)
	if isValid != nil {
		return fmt.Errorf("cache invalid: %w", isValid)
	}

	if freshness <= 0 && !hasMaxStale {
		c.Cache.Remove(cacheKey)
		return fmt.Errorf("cache stale")
	}

	// If request specifies max-age=100 and freshness is 350, the response is too fresh
	// We need to check: is the response older than maxAge?
	maxAge := reqCacheState.maxAge
	if reqCacheState.hasMaxAge && maxAge > 0 && age > maxAge {
		return fmt.Errorf("response older than request max-age")
	}

	serveCache(w, freshness, cacheKey, &cacheValue)
	return nil
}

// parseHeaderTime extracts a duration value from cache-control header.
// Supports both underscore and hyphen formats (e.g., max-age or max_age).
func parseHeaderTime(cc string, prefix string) time.Duration {
	if cc == "" {
		return 0
	}
	// e.g. max-age=N format (also supports max_age)
	// Try with hyphen first (standard format), then underscore (alternative format)
	for _, sep := range []string{"", "-"} {
		search := prefix + sep + "="
		if idx := strings.Index(cc, search); idx >= 0 {
			rest := cc[idx+len(search):]
			// Find the end of the number (comma or end of string)
			end := len(rest)
			for i, ch := range rest {
				if ch == ',' || ch == ' ' {
					end = i
					break
				}
			}
			// Parse the number
			var age int64
			_, _ = fmt.Sscanf(rest[:end], "%d", &age)
			return time.Duration(age) * time.Second
		}
	}
	return 0
}

// RFC 9111 4.2.1 Calculating Freshness
// https://www.rfc-editor.org/rfc/rfc9111#section-4.2.1
func calcFreshness(state cacheControlState, expires time.Time, age time.Duration, defaultTtl time.Duration) time.Duration {
	ttl := defaultTtl
	smaxAgeDur := state.sharedMaxAge
	maxAgeDur := state.maxAge
	remExpires := time.Until(expires)

	if smaxAgeDur.Seconds() > 0 {
		ttl = smaxAgeDur
	} else if maxAgeDur.Seconds() > 0 {
		ttl = maxAgeDur
	} else if remExpires > 0 {
		ttl = remExpires
	}

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

func cacheStatusStale(cacheKey string, originStatus int) string {
	return fmt.Sprintf("pico; fwd=stale; fwd-status=%d", originStatus)
}

func cacheStatusMiss(cacheKey string, stored bool) string {
	// RFC 9211 2.2 Cache-Status fwd
	// https://www.rfc-editor.org/rfc/rfc9211#section-2.2
	status := "pico; fwd=uri-miss"
	if stored {
		// RFC 9211 2.2 Cache-Status stored
		// https://www.rfc-editor.org/rfc/rfc9211#section-2.5
		status = fmt.Sprintf("%s; stored", status)
	}
	// RFC 9222 2.7 Cache-status key
	// https://www.rfc-editor.org/rfc/rfc9211#section-2.7
	status = fmt.Sprintf("%s; key=%s", status, cacheKey)
	return status
}

func stripForbiddenHeaders(w http.ResponseWriter, cacheValue *CacheValue) http.Header {
	hdr := w.Header()
	for key, values := range cacheValue.Header {
		if isForbiddenHeader(key) {
			continue
		}
		hdr[http.CanonicalHeaderKey(key)] = values
	}
	return hdr
}
