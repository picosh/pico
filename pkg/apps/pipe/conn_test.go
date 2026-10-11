package pipe

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/picosh/utils/pipe"
	"golang.org/x/crypto/ssh"
)

// closers closes what a test server opened once the test ends.
type closers struct {
	mu sync.Mutex
	cs []io.Closer
}

func newClosers(t *testing.T) *closers {
	c := &closers{}
	t.Cleanup(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, cl := range c.cs {
			_ = cl.Close()
		}
	})
	return c
}

func (c *closers) add(cl io.Closer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cs = append(c.cs, cl)
}

// stalledConn is a pipe connection to a server that accepts connections and
// never answers.
func stalledConn(t *testing.T, timeout time.Duration) *pipeConn {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	open := newClosers(t)
	open.add(ln)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			open.add(conn)
		}
	}()
	conn := newPipeConn(&pipe.SSHClientInfo{RemoteHost: ln.Addr().String()})
	conn.timeout = timeout
	return conn
}

func TestStalledHandshakeEndsWithRequest(t *testing.T) {
	conn := stalledConn(t, time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := conn.run(ctx, "sub topic"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run = %v, want the request's deadline", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("canceled request waited %v for the handshake", took)
	}

	// The next request is not held up behind the canceled one.
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start = time.Now()
	if _, err := conn.run(ctx, "sub topic"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run = %v, want the request's deadline", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("second request waited %v", took)
	}
}

func TestStalledHandshakeTimesOut(t *testing.T) {
	conn := stalledConn(t, 200*time.Millisecond)

	start := time.Now()
	_, err := conn.run(context.Background(), "sub topic")
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("run = %v, want a timeout", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("handshake took %v, want about 200ms", took)
	}
}

// stalledSSH is a pipe connection to an ssh server that completes the
// handshake and then stops answering: at opening a session when openStalls
// is set, or else at starting the command. It counts the connections made
// and the ones closed.
func stalledSSH(t *testing.T, timeout time.Duration, openStalls bool) (conn *pipeConn, conns, closed *atomic.Int32) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(hostKey)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	open := newClosers(t)
	open.add(ln)
	conns, closed = &atomic.Int32{}, &atomic.Int32{}
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			open.add(nc)
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go func() {
					_ = sc.Wait()
					closed.Add(1)
				}()
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					if openStalls {
						continue
					}
					ch, chReqs, err := nch.Accept()
					if err != nil {
						return
					}
					open.add(ch)
					// Never reply to the exec request.
					go func() {
						for range chReqs {
						}
					}()
				}
			}()
		}
	}()
	conn = newPipeConn(&pipe.SSHClientInfo{RemoteHost: ln.Addr().String()})
	conn.timeout = timeout
	return conn, conns, closed
}

func TestStalledSessionEndsWithRequest(t *testing.T) {
	for name, openStalls := range map[string]bool{"open": true, "exec": false} {
		t.Run(name, func(t *testing.T) {
			conn, _, _ := stalledSSH(t, time.Minute, openStalls)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			start := time.Now()
			if _, err := conn.run(ctx, "sub topic"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("run = %v, want the request's deadline", err)
			}
			if took := time.Since(start); took > time.Second {
				t.Errorf("canceled request waited %v for the session", took)
			}
		})
	}
}

func TestStalledSessionRedials(t *testing.T) {
	for name, openStalls := range map[string]bool{"open": true, "exec": false} {
		t.Run(name, func(t *testing.T) {
			conn, conns, _ := stalledSSH(t, 200*time.Millisecond, openStalls)
			_, err := conn.run(context.Background(), "sub topic")
			if err == nil || !strings.Contains(err.Error(), "timed out") {
				t.Fatalf("run = %v, want a timeout", err)
			}
			if n := conns.Load(); n != 2 {
				t.Errorf("made %d connections, want the stalled one and a redial", n)
			}
		})
	}
}

func TestCanceledStartStillTimesOut(t *testing.T) {
	for name, openStalls := range map[string]bool{"open": true, "exec": false} {
		t.Run(name, func(t *testing.T) {
			conn, _, closed := stalledSSH(t, 300*time.Millisecond, openStalls)
			before := runtime.NumGoroutine()
			for range 5 {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				_, err := conn.run(ctx, "sub topic")
				cancel()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("run = %v, want the request's deadline", err)
				}
			}
			for range 100 {
				if closed.Load() == 1 && runtime.NumGoroutine() <= before {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Errorf("closed %d connections and have %d goroutines, want 1 and at most %d", closed.Load(), runtime.NumGoroutine(), before)
		})
	}
}
