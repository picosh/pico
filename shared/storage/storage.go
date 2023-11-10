package storage

import (
	"os"

	"github.com/picosh/pico/wish/send/utils"
)

type Bucket struct {
	Name string
	Path string
}

type ObjectStorage interface {
	GetBucket(name string) (Bucket, error)
	UpsertBucket(name string) (Bucket, error)

	DeleteBucket(bucket Bucket) error
	GetBucketQuota(bucket Bucket) (uint64, error)
	GetFile(bucket Bucket, fpath string) (utils.ReaderAtCloser, int64, error)
	PutFile(bucket Bucket, fpath string, contents utils.ReaderAtCloser) (string, error)
	DeleteFile(bucket Bucket, fpath string) error
	ListFiles(bucket Bucket, dir string, recursive bool) ([]os.FileInfo, error)
}
