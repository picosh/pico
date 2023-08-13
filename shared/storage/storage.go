package storage

import (
	"io"
	"os"
)

type Bucket struct {
	Name string
	Path string
}

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

type ObjectStorage interface {
	GetBucket(name string) (Bucket, error)
	UpsertBucket(name string) (Bucket, error)

	DeleteBucket(bucket Bucket) error
	GetBucketQuota(bucket Bucket) (uint64, error)
	GetFile(bucket Bucket, fpath string) (ReaderAtCloser, error)
	PutFile(bucket Bucket, fpath string, contents ReaderAtCloser) (string, error)
	DeleteFile(bucket Bucket, fpath string) error
	ListFiles(bucket Bucket, dir string, recursive bool) ([]os.FileInfo, error)
}
