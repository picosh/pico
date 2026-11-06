package pgs

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/netguard"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
)

func proxyTestRouter(t *testing.T, target string, transport http.RoundTripper) (*WebRouter, *Config) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	database := NewPgsDb(logger)
	bucket := shared.GetAssetBucketName(database.Users[0].ID)
	st, err := storage.NewStorageMemory(map[string]map[string]string{
		bucket: {"/test/_redirects": "/proxy " + target + " 200!"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(logger, database, st, NewPubsubChan())
	cfg.Domain = "pgs.test"
	cfg.ProxyTransport = transport
	return NewWebRouter(cfg), cfg
}

func publicProxyFixture(t *testing.T, server *httptest.Server) *netguard.Transport {
	t.Helper()
	transport := netguard.NewTransport()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return netguard.Dialer().DialContext(ctx, network, address)
		}
		if err := netguard.ValidateIP(ip); err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	t.Cleanup(transport.CloseIdleConnections)
	return transport
}

func TestExternalProxyBlocksPrivateDestinations(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "private response")
	}))
	defer server.Close()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	for _, host := range []string{"127.0.0.1", "localhost", "[::1]", "[::ffff:127.0.0.1]", "169.254.169.254", "172.101.0.1"} {
		t.Run(host, func(t *testing.T) {
			router, _ := proxyTestRouter(t, "http://"+host+":"+port+"/private", publicProxyFixture(t, server))
			res := httptest.NewRecorder()
			router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "https://testusr-test.pgs.test/proxy", nil))
			if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "blocked") {
				t.Fatalf("status %d: %s", res.Code, res.Body)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("private upstream received a request")
	}
}

func TestExternalProxyPreservesPublicResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/content?version=2" || r.Host != "8.8.8.8" || r.Header.Get("X-Fixture") != "kept" {
			t.Errorf("unexpected upstream request: %s %s %+v", r.Host, r.URL, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"public":"asset"}`)
	}))
	defer server.Close()
	router, _ := proxyTestRouter(t, "http://8.8.8.8/content", publicProxyFixture(t, server))
	req := httptest.NewRequest(http.MethodGet, "https://testusr-test.pgs.test/proxy?version=2", nil)
	req.Header.Set("X-Fixture", "kept")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != `{"public":"asset"}` || res.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, headers %v, body %s", res.Code, res.Header(), res.Body)
	}
}

func TestExternalProxyTimeoutAndSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "larger than the configured limit")
	}))
	defer server.Close()
	for _, path := range []string{"/slow", "/large"} {
		t.Run(path, func(t *testing.T) {
			router, cfg := proxyTestRouter(t, "http://8.8.8.8"+path, publicProxyFixture(t, server))
			cfg.ProxyTimeout = 100 * time.Millisecond
			cfg.ProxyMaxSize = 8
			res := httptest.NewRecorder()
			router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "https://testusr-test.pgs.test/proxy", nil))
			if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "8.8.8.8"+path) {
				t.Fatalf("status %d: %s", res.Code, res.Body)
			}
		})
	}
}

func TestExternalProxyAbortsOversizedChunkedResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, strings.Repeat("x", 1024))
	}))
	defer upstream.Close()
	router, cfg := proxyTestRouter(t, "http://8.8.8.8/content", publicProxyFixture(t, upstream))
	cfg.ProxyMaxSize = 8
	server := httptest.NewServer(router)
	defer server.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/proxy", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "testusr-test.pgs.test"
	resp, err := server.Client().Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err == nil || len(body) > 8 {
		t.Fatalf("expected truncated stream within limit, got %d bytes and %v", len(body), err)
	}
}
