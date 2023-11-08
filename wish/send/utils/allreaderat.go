package utils

import (
	"errors"
	"io"
	"net/http"

	"github.com/minio/minio-go/v7"
)

type AllReaderAt struct {
	Reader io.ReaderAt
}

func NewAllReaderAt(reader io.ReaderAt) *AllReaderAt {
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
