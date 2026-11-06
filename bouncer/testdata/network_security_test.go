package soju

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPicoUpstreamBlocksPrivateAddresses(t *testing.T) {
	u := &user{srv: NewServer(nil)}
	for _, address := range []string{"127.0.0.1:6667", "localhost:6667", "[::1]:6667", "[::ffff:127.0.0.1]:6667", "169.254.169.254:80", "10.0.0.1:6667", "172.101.0.1:6667"} {
		conn, err := dialTCP(context.Background(), u, address)
		if err == nil {
			_ = conn.Close()
			t.Fatalf("accepted %s", address)
		}
		if !strings.Contains(err.Error(), "blocked") {
			t.Errorf("%s: %v", address, err)
		}
	}
}

func TestPicoWebPushBlocksPrivateAddresses(t *testing.T) {
	client := buildWebPushHTTPClient()
	defer client.CloseIdleConnections()
	for _, endpoint := range []string{"https://127.0.0.1/push", "https://localhost/push", "https://[::ffff:10.0.0.1]/push", "https://169.254.169.254/push"} {
		resp, err := client.Get(endpoint)
		if err == nil {
			_ = resp.Body.Close()
			t.Fatalf("accepted %s", endpoint)
		}
		if !strings.Contains(err.Error(), "blocked") {
			t.Errorf("%s: %v", endpoint, err)
		}
	}
}

func TestPicoWebPushRedirectPolicy(t *testing.T) {
	client := buildWebPushHTTPClient()
	req := httptest.NewRequest(http.MethodGet, "https://example.com/push", nil)
	if err := client.CheckRedirect(req, nil); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"http://example.com/push", "https://127.0.0.1/push"} {
		req := httptest.NewRequest(http.MethodGet, endpoint, nil)
		if err := client.CheckRedirect(req, nil); err == nil {
			t.Fatalf("accepted redirect to %s", endpoint)
		}
	}
	if client.Timeout == 0 {
		t.Fatal("Web Push client has no timeout")
	}
}

func TestPicoUpstreamKeepsSourceAddressSupport(t *testing.T) {
	srv := NewServer(nil)
	_, source, err := net.ParseCIDR("127.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	srv.SetConfig(&Config{UpstreamUserIPs: []*net.IPNet{source}})
	u := &user{srv: srv}
	conn, err := dialTCP(context.Background(), u, "127.0.0.1:6667")
	if err == nil {
		_ = conn.Close()
		t.Fatal("source-address selection bypassed the destination filter")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("source-address selection failed before the destination check: %v", err)
	}
}
