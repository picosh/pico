package netguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// Some local destinations use globally unicast address space.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	// Azure routes this address to platform services.
	netip.MustParsePrefix("168.63.129.16/32"),
	// Production's default Docker bridge uses public address space.
	netip.MustParsePrefix("172.101.0.0/24"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

var publicIPv6 = netip.MustParsePrefix("2000::/3")

func ValidateIP(ip netip.Addr) error {
	zoned := ip.Zone() != ""
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || zoned || (ip.Is6() && !publicIPv6.Contains(ip)) {
		return fmt.Errorf("destination %s is blocked: a public IP address is required", ip)
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(ip) {
			return fmt.Errorf("destination %s is blocked: a public IP address is required", ip)
		}
	}
	return nil
}

func ValidateHTTPURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL must use http or https")
	}
	if u.Hostname() == "" || u.Opaque != "" {
		return fmt.Errorf("URL must include a hostname")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
		return ValidateIP(ip)
	}
	return nil
}

func Dialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		ControlContext: func(_ context.Context, _, address string, _ syscall.RawConn) error {
			// Check the resolved socket address so DNS changes cannot bypass validation.
			addr, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("invalid destination: %w", err)
			}
			return ValidateIP(addr.Addr())
		},
	}
}

type Transport struct {
	*http.Transport
}

func NewTransport() *Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	// Requests must reach the validated destination directly, including redirects.
	t.Proxy = nil
	t.DialContext = Dialer().DialContext
	t.ResponseHeaderTimeout = 15 * time.Second
	t.MaxIdleConnsPerHost = 4
	return &Transport{t}
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := ValidateHTTPURL(req.URL); err != nil {
		return nil, err
	}
	return t.Transport.RoundTrip(req)
}

func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return ValidateHTTPURL(req.URL)
}

func ReadBody(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds the %d byte limit", limit)
	}
	return body, nil
}

type limitedBody struct {
	io.ReadCloser
	remaining int64
}

func LimitBody(body io.ReadCloser, limit int64) io.ReadCloser {
	return &limitedBody{ReadCloser: body, remaining: limit}
}

var ErrResponseTooLarge = errors.New("response size limit exceeded")

func (b *limitedBody) Read(p []byte) (int, error) {
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	if int64(n) > b.remaining {
		n = int(b.remaining)
		err = ErrResponseTooLarge
	}
	b.remaining -= int64(n)
	return n, err
}
