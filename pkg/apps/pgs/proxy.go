package pgs

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/picosh/pico/pkg/netguard"
)

const (
	defaultProxyTimeout = 2 * time.Minute
	defaultProxyMaxSize = 100 * 1024 * 1024
)

var publicProxyTransport = netguard.NewTransport()

func (h *ApiAssetHandler) proxyExternal(w http.ResponseWriter, r *http.Request, dest *url.URL) {
	timeout := h.Cfg.ProxyTimeout
	if timeout <= 0 {
		timeout = defaultProxyTimeout
	}
	maxSize := h.Cfg.ProxyMaxSize
	if maxSize <= 0 || maxSize == 1<<63-1 {
		maxSize = defaultProxyMaxSize
	}
	transport := h.Cfg.ProxyTransport
	if transport == nil {
		transport = publicProxyTransport
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		ErrorLog:  slog.NewLogLogger(h.Logger.With("destination", dest.Redacted()).Handler(), slog.LevelError),
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(dest)
			req.Out.URL.Path = dest.Path
			req.Out.URL.RawPath = dest.RawPath
			req.Out.URL.RawQuery = dest.RawQuery
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Set("cache-control", "no-cache")
			if resp.ContentLength > maxSize {
				return fmt.Errorf("upstream HTTP %s: response exceeds the %d byte proxy limit", resp.Status, maxSize)
			}
			if resp.StatusCode != http.StatusSwitchingProtocols {
				resp.Body = netguard.LimitBody(resp.Body, maxSize)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			h.Logger.Error("external proxy failed", "destination", dest.Redacted(), "err", err)
			http.Error(w, fmt.Sprintf("proxying %s: %v", dest.Redacted(), err), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r.WithContext(ctx))
}
