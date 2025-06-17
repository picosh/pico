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

	"github.com/darkweak/souin/pkg/middleware"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/picosh/pico/pkg/apps/pgs"
	"github.com/picosh/pico/pkg/cache"
	"github.com/picosh/pico/pkg/shared"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	logger := shared.CreateLogger("pgs-cdn")
	ctx := context.Background()
	cfg := pgs.NewPgsConfig(logger, nil, nil)
	httpCache := pgs.SetupCache(cfg)
	router := &pgs.WebRouter{
		Cfg:            cfg,
		RedirectsCache: expirable.NewLRU[string, []*pgs.RedirectRule](2048, nil, cache.CacheTimeout),
		HeadersCache:   expirable.NewLRU[string, []*pgs.HeaderRule](2048, nil, cache.CacheTimeout),
	}
	cacher := &cachedHttp{
		handler: httpCache,
		routes:  router,
	}

	go router.WatchCacheClear()
	go router.CacheMgmt(ctx, httpCache, cfg.CacheClearingQueue)

	portStr := fmt.Sprintf(":%s", cfg.WebPort)
	cfg.Logger.Info(
		"starting server on port",
		"port", cfg.WebPort,
		"domain", cfg.Domain,
	)
	err := http.ListenAndServe(portStr, cacher)
	cfg.Logger.Error("listen and serve", "err", err)
}

type cachedHttp struct {
	handler *middleware.SouinBaseHandler
	routes  *pgs.WebRouter
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

func (c *cachedHttp) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/_metrics" {
		promhttp.Handler().ServeHTTP(writer, req)
		return
	}

	if req.URL.Path == "/check" {
		c.routes.Cfg.Logger.Info("proxying `/check` request to ash.pgs.sh", "query", req.URL.RawQuery)
		req, _ := http.NewRequest("GET", "https://ash.pgs.sh/check?"+req.URL.RawQuery, nil)
		req.Host = "pgs.sh"
		// reqDump, _ := httputil.DumpRequestOut(req, true)
		// fmt.Printf("REQUEST:\n%s", string(reqDump))

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			c.routes.Cfg.Logger.Error("check request", "err", err)
		}
		writer.WriteHeader(resp.StatusCode)
		return
	}

	_ = c.handler.ServeHTTP(writer, req, func(w http.ResponseWriter, r *http.Request) error {
		url, _ := url.Parse(partialURL(r))

		c.routes.Cfg.Logger.Info("proxying request to ash.pgs.sh", "url", url.String())
		defaultTransport := http.DefaultTransport.(*http.Transport)
		oldDialContext := defaultTransport.DialContext
		newTransport := CustomTransport{Transport: defaultTransport, Logger: c.routes.Cfg.Logger}
		newTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return oldDialContext(ctx, "tcp", "ash.pgs.sh:443")
		}
		proxy := httputil.NewSingleHostReverseProxy(url)
		proxy.Transport = &newTransport
		proxy.ServeHTTP(w, r)
		return nil
	})
}

func partialURL(r *http.Request) string {
	builder := strings.Builder{}
	// this service sits behind a proxy so we need to force it to https
	builder.WriteString("https://")
	builder.WriteString(r.Host)
	return builder.String()
}
