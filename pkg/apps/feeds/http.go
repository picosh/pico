package feeds

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/picosh/pico/pkg/netguard"
)

const (
	feedRequestTimeout = 30 * time.Second
	feedDigestTimeout  = 2 * time.Minute
	defaultFeedMaxSize = 10 * 1024 * 1024
)

type UserAgentTransport struct {
	http.RoundTripper
}

func (c *UserAgentTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("User-Agent", "linux:feeds:v2 (by /u/pico-sh)")
	r.Header.Set("Accept", "*/*")
	return c.RoundTripper.RoundTrip(r)
}

func newFeedHTTPClient() *http.Client {
	transport := netguard.NewTransport()
	transport.MaxConnsPerHost = 4
	return &http.Client{
		Transport:     &UserAgentTransport{transport},
		Timeout:       feedRequestTimeout,
		CheckRedirect: netguard.CheckRedirect,
	}
}

var feedHTTPClient = newFeedHTTPClient()

func (f *Fetcher) ParseURL(ctx context.Context, fp *gofeed.Parser, rawURL string) (*gofeed.Feed, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid feed URL: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	details := fmt.Sprintf("%s: HTTP %s (content type %q)", resp.Request.URL.Redacted(), resp.Status, resp.Header.Get("Content-Type"))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s", details)
	}

	maxSize := f.cfg.FetchMaxSize
	if maxSize <= 0 || maxSize == 1<<63-1 {
		maxSize = defaultFeedMaxSize
	}
	tooLarge := func() error {
		return fmt.Errorf("%s: response exceeds the %d byte feed limit", details, maxSize)
	}
	if resp.ContentLength > maxSize {
		return nil, tooLarge()
	}
	// Drain within the limit so early parser returns still enforce size and reuse connections.
	body := &io.LimitedReader{R: resp.Body, N: maxSize + 1}
	feed, parseErr := fp.Parse(body)
	if body.N == 0 {
		return nil, tooLarge()
	}
	if parseErr != nil {
		return nil, fmt.Errorf("%s: parsing feed: %w", details, parseErr)
	}
	_, readErr := io.Copy(io.Discard, body)
	if body.N == 0 {
		return nil, tooLarge()
	}
	if readErr != nil {
		return nil, fmt.Errorf("%s: reading feed: %w", details, readErr)
	}
	return feed, nil
}
