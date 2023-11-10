package utils

import (
	"errors"
	"io"
	"net/http"

	"github.com/minio/minio-go/v7"
)

type ReadAndReaderAt interface {
	io.ReaderAt
	io.Reader
}

type ReaderAtCloser interface {
	io.ReaderAt
	io.ReadCloser
}

func NopReaderAtCloser(r ReadAndReaderAt) ReaderAtCloser {
	return nopReaderAtCloser{r}
}

type nopReaderAtCloser struct {
	ReadAndReaderAt
}

func (nopReaderAtCloser) Close() error { return nil }

type AllReaderAt struct {
	Reader ReaderAtCloser
}

func NewAllReaderAt(reader ReaderAtCloser) *AllReaderAt {
	return &AllReaderAt{reader}
}

func (a *AllReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = a.Reader.ReadAt(p, off)

	if errors.Is(err, io.EOF) {
		return
	}

	resp := minio.ToErrorResponse(err)

	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		err = io.EOF
	}

	return
}

func (a *AllReaderAt) Read(p []byte) (int, error) {
	return a.Reader.Read(p)
}

func (a *AllReaderAt) Close() error {
	return a.Reader.Close()
}
