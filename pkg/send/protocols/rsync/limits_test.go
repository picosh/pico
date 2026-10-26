package rsync

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/picosh/pico/pkg/pssh"
)

func TestTransferLimits(t *testing.T) {
	l := transferLimiter{users: make(map[string]int)}
	for range maxTransfersPerUser {
		if !l.acquire("account") {
			t.Fatal("rejected transfer below account limit")
		}
	}
	if l.acquire("account") {
		t.Fatal("accepted transfer above account limit")
	}
	l.release("account")
	if !l.acquire("account") {
		t.Fatal("released account slot was unavailable")
	}
	for i := maxTransfersPerUser; i < maxTransfers; i++ {
		if !l.acquire(fmt.Sprint(i)) {
			t.Fatal("rejected transfer below global limit")
		}
	}
	if l.acquire("another-account") {
		t.Fatal("accepted transfer above global limit")
	}
	l.release("account")
	if !l.acquire("another-account") {
		t.Fatal("released global slot was unavailable")
	}
}

func TestTransferLimitsConcurrent(t *testing.T) {
	l := transferLimiter{users: make(map[string]int)}
	var ready, done sync.WaitGroup
	var accepted atomic.Int32
	release := make(chan struct{})
	for range 64 {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ok := l.acquire("account")
			if ok {
				accepted.Add(1)
			}
			ready.Done()
			<-release
			if ok {
				l.release("account")
			}
		}()
	}
	ready.Wait()
	close(release)
	done.Wait()
	if accepted.Load() != maxTransfersPerUser || l.active != 0 || len(l.users) != 0 {
		t.Fatalf("accepted=%d, active=%d, accounts=%d", accepted.Load(), l.active, len(l.users))
	}
}

func TestMiddlewareRejectsExcessTransfers(t *testing.T) {
	const account = "limit-test-account"
	for range maxTransfersPerUser {
		if !transfers.acquire(account) {
			t.Fatal("could not reserve test slot")
		}
		t.Cleanup(func() { transfers.release(account) })
	}
	s := newMockSession()
	defer func() { _ = s.Close() }()
	s.Permissions().Extensions["user_id"] = account
	s.SetValue("command", []string{"rsync", "--server", ".", "site"})
	handler := &mockWriteHandler{}
	called := false
	mdw := Middleware(handler)(func(*pssh.SSHServerConnSession) error { called = true; return nil })
	if err := mdw(s); err != nil {
		t.Fatal(err)
	}
	stderr := s.Channel.(*mockChannel).stderr.String()
	if called || len(handler.calls) != 0 || !strings.Contains(stderr, "transfer limit") {
		t.Fatalf("rejected transfer reached handler: called=%v, storage=%v, stderr=%q", called, handler.calls, stderr)
	}
}

func TestMiddlewareReleasesFailedTransfer(t *testing.T) {
	const account = "failed-transfer-account"
	s := newMockSession()
	defer func() { _ = s.Close() }()
	s.Permissions().Extensions["user_id"] = account
	s.SetValue("command", []string{"rsync", "--server", "--block-size=2147483647", ".", "file"})
	mdw := Middleware(&mockWriteHandler{})(func(*pssh.SSHServerConnSession) error { return nil })
	if err := mdw(s); err != nil {
		t.Fatal(err)
	}
	if transfers.users[account] != 0 {
		t.Fatal("failed transfer retained its account slot")
	}
}

func TestIdleTransferInterruptsIO(t *testing.T) {
	for _, write := range []bool{false, true} {
		t.Run(fmt.Sprintf("write=%v", write), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				local, peer := net.Pipe()
				defer func() { _ = peer.Close() }()
				stream := newIdleReadWriter(local, time.Minute, func() { _ = local.Close() })
				defer stream.stop()
				done := make(chan error, 1)
				go func() {
					var err error
					if write {
						_, err = stream.Write([]byte{1})
					} else {
						_, err = stream.Read(make([]byte, 1))
					}
					done <- err
				}()
				time.Sleep(time.Minute)
				synctest.Wait()
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("idle IO succeeded")
					}
				default:
					t.Fatal("idle IO remained blocked")
				}
			})
		})
	}
}

func TestIdleTransferActivity(t *testing.T) {
	for _, write := range []bool{false, true} {
		t.Run(fmt.Sprintf("write=%v", write), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				local, peer := net.Pipe()
				defer func() { _ = local.Close() }()
				defer func() { _ = peer.Close() }()
				aborted := make(chan struct{})
				stream := newIdleReadWriter(local, time.Minute, func() { close(aborted); _ = local.Close() })
				defer stream.stop()
				for range 4 {
					time.Sleep(45 * time.Second)
					peerDone := make(chan error, 1)
					go func() {
						var err error
						if write {
							_, err = peer.Read(make([]byte, 1))
						} else {
							_, err = peer.Write([]byte{1})
						}
						peerDone <- err
					}()
					var err error
					if write {
						_, err = stream.Write([]byte{1})
					} else {
						_, err = stream.Read(make([]byte, 1))
					}
					if err != nil {
						t.Fatalf("active stream failed: %v", err)
					}
					if err := <-peerDone; err != nil {
						t.Fatal(err)
					}
				}
				time.Sleep(time.Minute)
				synctest.Wait()
				select {
				case <-aborted:
				default:
					t.Fatal("idle stream survived its deadline")
				}
			})
		})
	}
}

func TestIdleTransferStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		aborted := false
		stream := newIdleReadWriter(&bytes.Buffer{}, time.Minute, func() { aborted = true })
		stream.stop()
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if aborted {
			t.Fatal("completed transfer was aborted")
		}
	})
}

func TestIdleTransferLargeWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		local, peer := net.Pipe()
		defer func() { _ = local.Close() }()
		defer func() { _ = peer.Close() }()
		stream := newIdleReadWriter(local, time.Minute, func() { _ = local.Close() })
		defer stream.stop()
		done := make(chan error, 1)
		go func() {
			buf := make([]byte, 32*1024)
			for range 3 {
				time.Sleep(45 * time.Second)
				if _, err := io.ReadFull(peer, buf); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
		payload := make([]byte, 3*32*1024)
		if n, err := stream.Write(payload); err != nil || n != len(payload) {
			t.Fatalf("large active write = %d, %v", n, err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}
