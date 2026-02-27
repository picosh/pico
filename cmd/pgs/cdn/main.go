package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/picosh/pico/pkg/apps/pgs"
	"github.com/picosh/pico/pkg/shared"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"
	logger := shared.CreateLogger("pgs-cdn", withPipe)
	ctx := context.Background()
	drain := pgs.CreateSubCacheDrain(ctx, logger)
	pubsub := pgs.NewPubsubPipe(drain)
	defer func() {
		_ = pubsub.Close()
	}()
	cfg := pgs.NewPgsConfig(logger, nil, nil, drain)
	proxy := &proxyServe{cfg.Logger}
	httpCache := pgs.NewPgsHttpCache(cfg, proxy)
	cacher := &cachedHttp{}

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
	Logger *slog.Logger
}

func (p *proxyServe) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	url, _ := url.Parse(partialURL(req))

	p.Logger.Info("proxying request to ash.pgs.sh", "url", url.String())
	defaultTransport := http.DefaultTransport.(*http.Transport)
	oldDialContext := defaultTransport.DialContext
	newTransport := CustomTransport{Transport: defaultTransport, Logger: p.Logger}
	newTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return oldDialContext(ctx, "tcp", "ash.pgs.sh:443")
	}
	proxy := httputil.NewSingleHostReverseProxy(url)
	proxy.Transport = &newTransport
	proxy.ServeHTTP(w, req)
}

type cachedHttp struct {
	Logger *slog.Logger
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
		return
	}
}

func partialURL(r *http.Request) string {
	builder := strings.Builder{}
	// this service sits behind a proxy so we need to force it to https
	builder.WriteString("https://")
	builder.WriteString(r.Host)
	return builder.String()
}

type CustomTransport struct {
	*http.Transport
	Logger *slog.Logger
}

func (t *CustomTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// reqDump, _ := httputil.DumpRequestOut(request, false)
	// t.Logger.Info("request", "dump", string(reqDump))
	response, err := http.DefaultTransport.RoundTrip(request)

	// body, err := httputil.DumpResponse(response, false)
	// if err != nil {
	// 	// copying the response body did not work
	// 	return nil, err
	// }
	// t.Logger.Info("response", "dump", string(body))

	return response, err
}
