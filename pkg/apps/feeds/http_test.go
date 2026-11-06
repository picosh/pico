package feeds

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/picosh/pico/pkg/netguard"
)

const testFeed = `<?xml version="1.0"?><rss version="2.0"><channel><title>Example</title><link>https://example.com</link><description>News</description><item><guid>new</guid><title>Article</title><link>https://example.com/article</link></item></channel></rss>`

func TestFeedClientBlocksLocalDestinations(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, testFeed)
	}))
	defer server.Close()
	t.Setenv("HTTP_PROXY", server.URL)
	t.Setenv("HTTPS_PROXY", server.URL)
	client := newFeedHTTPClient()
	defer client.CloseIdleConnections()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	for _, host := range []string{"127.0.0.1", "localhost", "[::1]", "[::ffff:127.0.0.1]"} {
		resp, err := client.Get("http://" + host + ":" + port + "/rss")
		if err == nil {
			_ = resp.Body.Close()
			t.Fatalf("accepted %s", host)
		}
		if !strings.Contains(err.Error(), "blocked") {
			t.Errorf("%s: %v", host, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("request reached a local destination or proxy")
	}
}

// Public test destinations are routed to a local fixture after their IP is checked.
func fixtureFeedClient(t *testing.T, server *httptest.Server) (*http.Client, string) {
	t.Helper()
	client := newFeedHTTPClient()
	transport := client.Transport.(*UserAgentTransport).RoundTripper.(*netguard.Transport).Transport
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
	t.Cleanup(client.CloseIdleConnections)
	return client, "http://8.8.8.8"
}

func TestFeedRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/public":
			http.Redirect(w, r, "http://1.1.1.1/rss", http.StatusFound)
		case "/private":
			http.Redirect(w, r, "http://127.0.0.1/rss", http.StatusFound)
		case "/private-dns":
			http.Redirect(w, r, "http://localhost/rss", http.StatusFound)
		case "/file":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			if r.UserAgent() != "linux:feeds:v2 (by /u/pico-sh)" {
				t.Errorf("unexpected user agent %q", r.UserAgent())
			}
			_, _ = io.WriteString(w, testFeed)
		}
	}))
	defer server.Close()
	client, baseURL := fixtureFeedClient(t, server)
	f := &Fetcher{client: client, cfg: &Config{}}
	feed, err := f.ParseURL(t.Context(), gofeed.NewParser(), baseURL+"/public")
	if err != nil || feed.Title != "Example" {
		t.Fatalf("public redirect: %v, %v", feed, err)
	}
	for _, tt := range []struct{ path, want string }{
		{"/private", "blocked"}, {"/private-dns", "blocked"}, {"/file", "http or https"}, {"/loop", "10 redirects"},
	} {
		_, err := f.ParseURL(t.Context(), gofeed.NewParser(), baseURL+tt.path)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want %s", tt.path, err, tt.want)
		}
	}
}

func TestFeedResponseLimitsAndErrors(t *testing.T) {
	const limit = 512
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		switch r.URL.Path {
		case "/status", "/choices":
			code := http.StatusNotFound
			if r.URL.Path == "/choices" {
				code = http.StatusMultipleChoices
			}
			w.WriteHeader(code)
			_, _ = io.WriteString(w, "response body secret")
		case "/bad":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html>not a feed</html>")
		case "/length":
			w.Header().Set("Content-Length", "513")
		case "/chunked":
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, testFeed+strings.Repeat(" ", limit))
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			writer := gzip.NewWriter(w)
			_, _ = io.WriteString(writer, testFeed+strings.Repeat(" ", limit))
			_ = writer.Close()
		case "/exact":
			_, _ = io.WriteString(w, testFeed+strings.Repeat(" ", limit-len(testFeed)))
		default:
			_, _ = io.WriteString(w, testFeed)
		}
	}))
	defer server.Close()
	client, baseURL := fixtureFeedClient(t, server)
	f := &Fetcher{client: client, cfg: &Config{FetchMaxSize: limit}}
	for _, tt := range []struct{ path, want string }{
		{"/status", "404 Not Found"}, {"/choices", "300 Multiple Choices"}, {"/bad", "parsing feed"},
		{"/length", "512 byte feed limit"}, {"/chunked", "512 byte feed limit"}, {"/gzip", "512 byte feed limit"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			_, err := f.ParseURL(t.Context(), gofeed.NewParser(), baseURL+tt.path)
			if err == nil {
				t.Fatal("expected error")
			}
			for _, want := range []string{baseURL + tt.path, "content type", tt.want} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "response body secret") {
				t.Fatal("response body leaked through error")
			}
		})
	}
	feed, err := f.ParseURL(t.Context(), gofeed.NewParser(), baseURL+"/exact")
	if err != nil || feed.Title != "Example" {
		t.Fatalf("exact size limit: %v, %v", feed, err)
	}
}

func TestFeedRequestTimeoutAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/body" {
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	client, baseURL := fixtureFeedClient(t, server)
	client.Timeout = 100 * time.Millisecond
	f := &Fetcher{client: client, cfg: &Config{}}
	for _, path := range []string{"/headers", "/body"} {
		_, err := f.ParseURL(t.Context(), gofeed.NewParser(), baseURL+path)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s: %v, want deadline exceeded", path, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := f.ParseURL(ctx, gofeed.NewParser(), baseURL+"/headers")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancellation: %v", err)
	}
}

func TestFeedConnectionReuse(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, testFeed+strings.Repeat(" ", 8192))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	client, baseURL := fixtureFeedClient(t, server)
	f := &Fetcher{client: client, cfg: &Config{}}
	for range 2 {
		if _, err := f.ParseURL(t.Context(), gofeed.NewParser(), baseURL+"/rss"); err != nil {
			t.Fatal(err)
		}
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("used %d connections, want 1", got)
	}
}

func TestFeedTLSHostnameVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, testFeed)
	}))
	defer server.Close()
	client := newFeedHTTPClient()
	defer client.CloseIdleConnections()
	transport := client.Transport.(*UserAgentTransport).RoundTripper.(*netguard.Transport).Transport
	transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	f := &Fetcher{client: client, cfg: &Config{}}
	_, err := f.ParseURL(t.Context(), gofeed.NewParser(), "https://example.com/rss")
	if err != nil {
		t.Fatalf("valid TLS hostname: %v", err)
	}
	_, err = f.ParseURL(t.Context(), gofeed.NewParser(), "https://wrong.example/rss")
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("wrong TLS hostname: %v", err)
	}
}
