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

func (p *proxyServe) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	target, _ := url.Parse(partialURL(req))
	p.Logger.Info("proxying request to ash.pgs.sh", "url", target.String())

	proxyReq := req.Clone(req.Context())
	proxyReq.URL.Scheme = target.Scheme
	proxyReq.URL.Host = target.Host
	proxyReq.URL.Path = target.Path
	proxyReq.URL.RawQuery = target.RawQuery
	proxyReq.RequestURI = ""

	resp, err := p.transport.RoundTrip(proxyReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Set(k, v)
		}
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
		writer.WriteHeader(resp.StatusCode)
		defer func() {
			_ = resp.Body.Close()
		}()
		_, _ = io.Copy(writer, resp.Body)
	}

	c.Cache.ServeHTTP(writer, req)
}

func partialURL(r *http.Request) string {
	builder := strings.Builder{}
	// this service sits behind a proxy so we need to force it to https
	builder.WriteString("https://")
	builder.WriteString(r.Host)
	return builder.String()
}
