package utils

import (
	"io"
)

type ReadAndReaderAt interface {
	io.ReaderAt
	io.Reader
}

type ReadAndReaderAtCloser interface {
	io.ReaderAt
	io.ReadSeekCloser
}

func NopReadAndReaderAtCloser(r ReadAndReaderAt) ReadAndReaderAtCloser {
	return nopReadAndReaderAt{r}
}

type nopReadAndReaderAt struct {
	ReadAndReaderAt
}

func (nopReadAndReaderAt) Close() error                   { return nil }
func (nopReadAndReaderAt) Seek(int64, int) (int64, error) { return 0, nil }
