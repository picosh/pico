package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/picosh/pico/pkg/apps/pgs"
	"github.com/picosh/pico/pkg/httpcache"
	"github.com/picosh/pico/pkg/shared"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	pipeEnabled := shared.GetEnv("PICO_PIPE_ENABLED", "true")
	withPipe := strings.ToLower(pipeEnabled) == "true"
	logger := shared.CreateLogger("pgs-cdn", withPipe)
	ctx := context.Background()
	drain := pgs.CreateSubCacheDrain(ctx, logger)
	pubsub := pgs.NewPubsubPipe(drain)
	defer func() {
		_ = pubsub.Close()
	}()
	cfg := pgs.NewPgsConfig(logger, nil, nil, drain)
	proxy := newProxyServe(cfg.Logger)
	httpCache := pgs.NewPgsHttpCache(cfg, proxy)
	cacher := &cachedHttp{
		Logger: cfg.Logger,
		Cache:  httpCache,
	}

	go pgs.CacheMgmt(ctx, cfg.CacheClearingQueue, cfg, httpCache.Cache)

	portStr := fmt.Sprintf(":%s", cfg.WebPort)
	cfg.Logger.Info(
		"starting server on port",
		"port", cfg.WebPort,
		"domain", cfg.Domain,
	)
	err := http.ListenAndServe(portStr, cacher)
	cfg.Logger.Error("listen and serve", "err", err)
}

type proxyServe struct {
	Logger    *slog.Logger
	transport *http.Transport
}

func newProxyServe(logger *slog.Logger) *proxyServe {
	defaultTransport := http.DefaultTransport.(*http.Transport)
	oldDial := defaultTransport.DialContext
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return oldDial(ctx, "tcp", "ash.pgs.sh:443")
		},
	}
	return &proxyServe{Logger: logger, transport: transport}
}

// Headers that should be stripped from upstream responses because the CDN's
// cache layer will add its own versions.
var stripHeaders = map[string]bool{
	"age":          true,
	"cache-status": true,
	"date":         true,
}

func (p *proxyServe) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Dial ash.pgs.sh but keep the original Host header so ash.pgs.sh
	// can route the request to the correct subdomain (zmx.sh, etc.).
	// Without the Host header, ash.pgs.sh serves its default vhost (HTML).
	target, _ := url.Parse("https://ash.pgs.sh" + req.URL.Path)
	if req.URL.RawQuery != "" {
		target.RawQuery = req.URL.RawQuery
	}
	p.Logger.Info("proxying request to ash.pgs.sh", "url", target.String())

	proxyReq := req.Clone(req.Context())
	proxyReq.URL.Scheme = target.Scheme
	proxyReq.URL.Host = target.Host
	proxyReq.URL.Path = target.Path
	proxyReq.URL.RawQuery = target.RawQuery
	proxyReq.RequestURI = ""
	// Prevent the upstream from returning a compressed body. The CDN cache
	// stores a single representation per URL; Caddy handles per-client
	// encoding on the way out. If we forward Accept-Encoding, origin may
	// return zstd-compressed bytes that get cached and then served to
	// clients that never requested zstd.
	proxyReq.Header.Del("Accept-Encoding")
	// Preserve the original Host header so ash.pgs.sh routes correctly.
	proxyReq.Host = req.Host

	resp, err := p.transport.RoundTrip(proxyReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Copy headers from upstream, but strip cache-related headers that the
	// CDN's cache layer will regenerate
	for k, vals := range resp.Header {
		if stripHeaders[strings.ToLower(k)] {
			continue
		}
		w.Header()[k] = vals
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

type cachedHttp struct {
	Logger *slog.Logger
	Cache  *httpcache.HttpCache
}

func (c *cachedHttp) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/_metrics" {
		promhttp.Handler().ServeHTTP(writer, req)
		return
	}

	if req.URL.Path == "/check" {
		c.Logger.Info("proxying `/check` request to ash.pgs.sh", "query", req.URL.RawQuery)
		req, _ := http.NewRequest("GET", "https://ash.pgs.sh/check?"+req.URL.RawQuery, nil)
		req.Host = "pgs.sh"
		// reqDump, _ := httputil.DumpRequestOut(req, true)
		// fmt.Printf("REQUEST:\n%s", string(reqDump))

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			c.Logger.Error("check request", "err", err)
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		writer.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(writer, resp.Body)
		return
	}

	c.Cache.ServeHTTP(writer, req)
}
