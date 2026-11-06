package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/apps/pgs"
	"github.com/picosh/pico/pkg/metrics"
	"github.com/picosh/pico/pkg/netguard"
	"github.com/picosh/pico/pkg/shared"
	"github.com/prometheus/client_golang/prometheus"
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
	cfg := pgs.NewConfig(logger, nil, nil, drain)
	proxy := newProxyServe(cfg.Logger)
	httpCache, cache := pgs.NewPgsHttpCache(cfg, proxy)
	// The CDN can't look users up, but origin fails every site route for a
	// site that doesn't exist, so a success proves the owner is real.
	cacher := &cachedHttp{
		Logger:      cfg.Logger,
		Cache:       pgs.NewSiteMetrics(cfg, "pgs-cdn", true).Handler(httpCache),
		CheckClient: originCheckClient,
	}

	go pgs.CacheMgmt(ctx, cfg, cache, nil)
	metrics.RegisterServerInfo(prometheus.DefaultRegisterer, "pgs-cdn")

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
	transport http.RoundTripper
}

func newProxyServe(logger *slog.Logger) *proxyServe {
	return &proxyServe{Logger: logger, transport: netguard.NewTransport()}
}

var originCheckClient = &http.Client{
	Transport:     netguard.NewTransport(),
	Timeout:       5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
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
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Minute)
	defer cancel()
	proxyReq := req.Clone(ctx)
	proxyReq.URL.Scheme = "https"
	proxyReq.URL.Host = "ash.pgs.sh"
	proxyReq.RequestURI = ""
	p.Logger.Info("proxying request to ash.pgs.sh", "url", proxyReq.URL.String())
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
	Logger      *slog.Logger
	Cache       http.Handler
	CheckClient *http.Client
}

func (c *cachedHttp) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/_metrics" {
		promhttp.Handler().ServeHTTP(writer, req)
		return
	}

	if req.URL.Path == "/check" {
		c.Logger.Info("proxying `/check` request to ash.pgs.sh", "query", req.URL.RawQuery)
		upstream, err := http.NewRequestWithContext(req.Context(), http.MethodGet, "https://ash.pgs.sh/check", nil)
		if err != nil {
			http.Error(writer, "invalid origin check request", http.StatusBadGateway)
			return
		}
		upstream.URL.RawQuery = req.URL.RawQuery
		upstream.Host = "pgs.sh"
		resp, err := c.CheckClient.Do(upstream)
		if err != nil {
			c.Logger.Error("check request", "err", err)
			http.Error(writer, "origin check failed", http.StatusBadGateway)
			return
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			http.Error(writer, "origin check returned an unexpected redirect", http.StatusBadGateway)
			return
		}
		body, err := netguard.ReadBody(resp.Body, 64*1024)
		if err != nil {
			c.Logger.Error("reading check response", "err", err)
			http.Error(writer, "invalid origin check response", http.StatusBadGateway)
			return
		}
		writer.WriteHeader(resp.StatusCode)
		_, _ = writer.Write(body)
		return
	}

	c.Cache.ServeHTTP(writer, req)
}
