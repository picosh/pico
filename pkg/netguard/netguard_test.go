package netguard

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestResponseBodyLimits(t *testing.T) {
	for _, size := range []int{0, 7, 8, 9, 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			input := strings.Repeat("x", size)
			body, err := ReadBody(strings.NewReader(input), 8)
			if (err != nil) != (size > 8) || (size <= 8 && string(body) != input) {
				t.Fatalf("buffered body %q: %v", body, err)
			}
			reader := &closeTrackingBody{Reader: strings.NewReader(input)}
			limited := LimitBody(reader, 8)
			body, err = io.ReadAll(limited)
			if (err != nil) != (size > 8) || len(body) > 8 || (size <= 8 && string(body) != input) {
				t.Fatalf("streamed body %q: %v", body, err)
			}
			if size > 8 && !errors.Is(err, ErrResponseTooLarge) {
				t.Fatalf("wrong size error: %v", err)
			}
			_ = limited.Close()
			if !reader.closed {
				t.Fatal("underlying response body was not closed")
			}
		})
	}
}

type closeTrackingBody struct {
	io.Reader
	closed bool
}

func (b *closeTrackingBody) Close() error { b.closed = true; return nil }

func TestValidateIP(t *testing.T) {
	for _, addr := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "2001:4860:4860::8888", "::ffff:8.8.8.8"} {
		if err := ValidateIP(netip.MustParseAddr(addr)); err != nil {
			t.Errorf("public address %s: %v", addr, err)
		}
	}
	for _, addr := range []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254",
		"168.63.129.16", "172.22.0.1", "172.101.0.1", "::ffff:172.101.0.1", "192.168.0.1", "192.0.0.1", "192.0.2.1", "192.88.99.1", "198.18.0.1",
		"198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "64:ff9b::7f00:1", "100::1",
		"2001::1", "2001:db8::1", "2002:7f00:1::", "3fff::1", "fc00::1", "fe80::1%eth0",
		"ff02::1", "2606:4700:4700::1111%eth0", "4000::1",
	} {
		if err := ValidateIP(netip.MustParseAddr(addr)); err == nil {
			t.Errorf("accepted non-public address %s", addr)
		}
	}
}

func TestValidateHTTPURL(t *testing.T) {
	for _, raw := range []string{"https://example.com/rss", "http://example.com:8080/rss", "https://user:password@example.com/rss"} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateHTTPURL(u); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{"file:///etc/passwd", "ftp://example.com/feed", "/feed.xml", "http:example.com", "http:///feed", "http://example.com@127.0.0.1/feed", "http://[::ffff:127.0.0.1]/feed"} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateHTTPURL(u); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestDialerChecksResolvedAddress(t *testing.T) {
	var private atomic.Bool
	dialer := Dialer()
	dialer.Resolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer func() { _ = server.Close() }()
				var size [2]byte
				if _, err := io.ReadFull(server, size[:]); err != nil {
					return
				}
				query := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := io.ReadFull(server, query); err != nil {
					return
				}
				questionEnd := 12
				for questionEnd < len(query) && query[questionEnd] != 0 {
					questionEnd += int(query[questionEnd]) + 1
				}
				questionEnd += 5
				if questionEnd > len(query) {
					return
				}
				answer := append([]byte(nil), query[:questionEnd]...)
				binary.BigEndian.PutUint16(answer[2:4], 0x8180)
				clear(answer[6:12])
				if binary.BigEndian.Uint16(query[questionEnd-4:questionEnd-2]) == 1 {
					ip := []byte{8, 8, 8, 8}
					if private.Load() {
						ip = []byte{127, 0, 0, 1}
					}
					binary.BigEndian.PutUint16(answer[6:8], 1)
					// The answer points back to the query name using DNS compression.
					answer = append(answer, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4)
					answer = append(answer, ip...)
				}
				binary.BigEndian.PutUint16(size[:], uint16(len(answer)))
				_, _ = server.Write(append(size[:], answer...))
			}()
			return client, nil
		},
	}
	guard := dialer.ControlContext
	intercepted := errors.New("public connection intercepted")
	var publicDials int
	dialer.ControlContext = func(ctx context.Context, network, address string, conn syscall.RawConn) error {
		if err := guard(ctx, network, address, conn); err != nil {
			return err
		}
		publicDials++
		if address != "8.8.8.8:80" {
			t.Errorf("unexpected resolved address %s", address)
		}
		return intercepted
	}
	_, err := dialer.DialContext(context.Background(), "tcp4", "feed.test:80")
	if !errors.Is(err, intercepted) {
		t.Fatalf("public DNS answer: %v", err)
	}
	private.Store(true)
	_, err = dialer.DialContext(context.Background(), "tcp4", "feed.test:80")
	if err == nil || !strings.Contains(err.Error(), "blocked") || publicDials != 1 {
		t.Fatalf("changed DNS answer: %v, public dials %d", err, publicDials)
	}
}
