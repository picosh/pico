package utils

import (
	"io"
)

type ReadAndReaderAt interface {
	io.ReaderAt
	io.Reader
}

type ReadAndReaderAtCloser interface {
	io.Reader
	io.ReaderAt
	io.ReadCloser
}

func NopReadAndReaderAtCloser(r ReadAndReaderAt) ReadAndReaderAtCloser {
	return nopReadAndReaderAt{r}
}

type nopReadAndReaderAt struct {
	ReadAndReaderAt
}

func (nopReadAndReaderAt) Close() error { return nil }
