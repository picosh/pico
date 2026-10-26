package rsync

import (
	"io"
	"sync"
	"time"
)

const (
	maxTransfers        = 8
	maxTransfersPerUser = 2
	transferIdleTimeout = 2 * time.Minute
)

var transfers = transferLimiter{users: make(map[string]int)}

type transferLimiter struct {
	mu     sync.Mutex
	active int
	users  map[string]int
}

func (l *transferLimiter) acquire(user string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active >= maxTransfers || l.users[user] >= maxTransfersPerUser {
		return false
	}
	l.active++
	l.users[user]++
	return true
}

func (l *transferLimiter) release(user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active--
	l.users[user]--
	if l.users[user] == 0 {
		delete(l.users, user)
	}
}

// idleReadWriter interrupts blocked transport IO when the stream stops moving.
type idleReadWriter struct {
	io.ReadWriter
	mu      sync.Mutex
	timer   *time.Timer
	timeout time.Duration
	last    time.Time
	stopped bool
	abort   func()
}

func newIdleReadWriter(rw io.ReadWriter, timeout time.Duration, abort func()) *idleReadWriter {
	w := &idleReadWriter{ReadWriter: rw, timeout: timeout, last: time.Now(), abort: abort}
	w.timer = time.AfterFunc(timeout, w.expire)
	return w
}

func (w *idleReadWriter) Read(p []byte) (int, error) {
	n, err := w.ReadWriter.Read(p)
	if n > 0 {
		w.touch()
	}
	return n, err
}

func (w *idleReadWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n, err := w.ReadWriter.Write(p[:min(len(p), 32*1024)])
		if n > 0 {
			w.touch()
			total += n
			p = p[n:]
		}
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (w *idleReadWriter) touch() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.stopped {
		w.last = time.Now()
		w.timer.Reset(w.timeout)
	}
}

func (w *idleReadWriter) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
	w.timer.Stop()
}

func (w *idleReadWriter) expire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	if remaining := w.timeout - time.Since(w.last); remaining > 0 {
		w.timer.Reset(remaining)
		return
	}
	w.stopped = true
	w.abort()
}
