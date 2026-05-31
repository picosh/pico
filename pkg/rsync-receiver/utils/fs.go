package utils

import (
	"io"
	"os"
)

type ReaderAtCloser interface {
	io.Reader
	io.ReaderAt
	io.Closer
}

// File System: need to handle all type of files: regular, folder, symlink, etc.
type FS interface {
	Put(*ReceiverFile) (int64, error)
	List(string) ([]os.FileInfo, error)
	Read(*SenderFile) (os.FileInfo, ReaderAtCloser, error)
	Remove([]*ReceiverFile) error
}
