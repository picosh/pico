package router

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/picosh/pico/pkg/shared"
)

// CreatePProfRoutesMux serves the profiler on mux.
func CreatePProfRoutesMux(mux *http.ServeMux) {
	for _, method := range []string{"GET", "POST"} {
		mux.HandleFunc(method+" /debug/pprof/", pprof.Index)
		mux.HandleFunc(method+" /debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc(method+" /debug/pprof/profile", pprof.Profile)
		mux.HandleFunc(method+" /debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc(method+" /debug/pprof/trace", pprof.Trace)
	}
}

// Cors adds CORS headers to h's responses.
func Cors(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		CorsHeaders(w.Header())
		h(w, r)
	}
}

// CorsPreflight answers a CORS preflight request.
func CorsPreflight(w http.ResponseWriter, r *http.Request) {
	CorsHeaders(w.Header())
	w.WriteHeader(http.StatusOK)
}

func GetSubdomainFromRequest(r *http.Request, domain, space string) string {
	hostDomain := strings.ToLower(strings.Split(r.Host, ":")[0])
	appDomain := strings.ToLower(strings.Split(domain, ":")[0])

	if hostDomain != appDomain && strings.Contains(hostDomain, ".") {
		if strings.Contains(hostDomain, appDomain) {
			subdomain := strings.TrimSuffix(hostDomain, fmt.Sprintf(".%s", appDomain))
			return subdomain
		} else {
			subdomain := GetCustomDomain(hostDomain, space)
			return subdomain
		}
	}

	return ""
}

// NewHostRouter serves requests for users' sites with sites, and the rest
// with main. A site is a subdomain of domain, or a custom domain whose TXT
// record for txtPrefix names one. GetSubdomain returns the site's name. A
// nil sites serves everything with main.
func NewHostRouter(domain, txtPrefix string, main, sites http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sites == nil {
			main.ServeHTTP(w, r)
			return
		}
		subdomain := GetSubdomainFromRequest(r, domain, txtPrefix)
		if subdomain == "" {
			main.ServeHTTP(w, r)
			return
		}
		sites.ServeHTTP(w, r.WithContext(WithSubdomain(r.Context(), subdomain)))
	})
}

type ctxSubdomainKey struct{}

// WithSubdomain marks ctx as a request for the site subdomain.
func WithSubdomain(ctx context.Context, subdomain string) context.Context {
	return context.WithValue(ctx, ctxSubdomainKey{}, subdomain)
}

// GetSubdomain is the site a request is for, or "" for the main domain.
func GetSubdomain(r *http.Request) string {
	subdomain, _ := r.Context().Value(ctxSubdomainKey{}).(string)
	return subdomain
}

var txtCache = expirable.NewLRU[string, string](2048, nil, shared.CacheTimeout)

// txtMissCache remembers hosts without a TXT record for a short time, so
// unknown hosts don't cost a lookup per request and a record that was just
// added starts working quickly.
var txtMissCache = expirable.NewLRU[string, struct{}](2048, nil, 30*time.Second)

var lookupTXT = net.LookupTXT

// ErrNoTXTRecord is returned when a host has no TXT record for a service.
var ErrNoTXTRecord = errors.New("no TXT record")

// TXTRecordName is the record that points host at a site on space.
func TXTRecordName(host, space string) string {
	return fmt.Sprintf("_%s.%s", space, host)
}

// FQDN returns name with a trailing dot, so the resolver won't retry a name
// that doesn't exist under its own search domains.
func FQDN(name string) string {
	return strings.TrimSuffix(name, ".") + "."
}

// LookupTXTRecords asks DNS for host's records on space, bypassing the
// cache. No records returns ErrNoTXTRecord.
func LookupTXTRecords(host, space string) ([]string, error) {
	records, err := lookupTXT(FQDN(TXTRecordName(host, space)))
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return nil, ErrNoTXTRecord
	}
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, ErrNoTXTRecord
	}
	return records, nil
}

// LookupCustomDomain returns the site host's first record on space names,
// bypassing the cache.
func LookupCustomDomain(host, space string) (string, error) {
	records, err := LookupTXTRecords(host, space)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(records[0]), nil
}

func GetCustomDomain(host string, space string) string {
	txt := TXTRecordName(host, space)
	if record, found := txtCache.Get(txt); found {
		return record
	}
	if _, missed := txtMissCache.Get(txt); missed {
		return ""
	}

	record, err := LookupCustomDomain(host, space)
	if errors.Is(err, ErrNoTXTRecord) {
		txtMissCache.Add(txt, struct{}{})
	}
	if err != nil {
		return ""
	}
	txtCache.Add(txt, record)
	return record
}

func GetApiToken(r *http.Request) string {
	authHeader := r.Header.Get("authorization")
	if authHeader == "" {
		return ""
	}
	return strings.TrimPrefix(authHeader, "Bearer ")
}
