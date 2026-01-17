package pobj

import (
	"errors"
	"io"

	"github.com/picosh/pico/pkg/send/utils"
)

type AllReaderAt struct {
	Reader utils.ReadAndReaderAtCloser
}

func NewAllReaderAt(reader utils.ReadAndReaderAtCloser) *AllReaderAt {
	return &AllReaderAt{reader}
}

func (a *AllReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = a.Reader.ReadAt(p, off)

	if errors.Is(err, io.EOF) {
		return
	}

	return
}

func (a *AllReaderAt) Read(p []byte) (int, error) {
	return a.Reader.Read(p)
}

func (a *AllReaderAt) Close() error {
	return a.Reader.Close()
}
