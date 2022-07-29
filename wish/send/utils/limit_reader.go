package utils

import (
	"io"
	"sync"
)

func NewLimitReader(r io.Reader, limit int) io.Reader {
	return &LimitReader{
		r:    r,
		left: limit,
	}
}

type LimitReader struct {
	r io.Reader

	lock sync.Mutex
	left int
}

func (r *LimitReader) Read(b []byte) (int, error) {
	r.lock.Lock()
	defer r.lock.Unlock()

	if r.left <= 0 {
		return 0, io.EOF
	}
	if len(b) > r.left {
		b = b[0:r.left]
	}
	n, err := r.r.Read(b)
	r.left -= n
	return n, err
}
