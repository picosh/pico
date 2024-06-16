package shared

import (
	"errors"
	"fmt"
	"io"
)

// Throws an error if the reader is bigger than limit.
var ErrSizeExceeded = errors.New("stream size exceeded")

type MaxBytesReader struct {
	io.Reader // reader object
	Limit     int64
	N         int64 // max bytes remaining.
}

func NewMaxBytesReader(r io.Reader, limit int64) *MaxBytesReader {
	return &MaxBytesReader{r, limit, limit}
}

func (b *MaxBytesReader) Read(p []byte) (n int, err error) {
	if b.N <= 0 {
		err := fmt.Errorf("%w: %.2fmb", ErrSizeExceeded, BytesToMB(int(b.Limit)))
		return 0, err
	}

	if int64(len(p)) > b.N {
		p = p[0:b.N]
	}

	n, err = b.Reader.Read(p)
	b.N -= int64(n)
	return
}
