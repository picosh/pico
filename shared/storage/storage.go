package storage

import (
	"io"
	"os"
	"time"

	"github.com/picosh/send/send/utils"
)

type Bucket struct {
	Name string
	Path string
	Root string
}

type ObjectStorage interface {
	GetBucket(name string) (Bucket, error)
	UpsertBucket(name string) (Bucket, error)
	ListBuckets() ([]string, error)

	DeleteBucket(bucket Bucket) error
	GetBucketQuota(bucket Bucket) (uint64, error)
	GetFileSize(bucket Bucket, fpath string) (int64, error)
	GetFile(bucket Bucket, fpath string) (utils.ReaderAtCloser, int64, time.Time, error)
	ServeFile(bucket Bucket, fpath string, ratio *Ratio, original bool, useProxy bool) (io.ReadCloser, string, error)
	PutFile(bucket Bucket, fpath string, contents utils.ReaderAtCloser, entry *utils.FileEntry) (string, error)
	DeleteFile(bucket Bucket, fpath string) error
	ListFiles(bucket Bucket, dir string, recursive bool) ([]os.FileInfo, error)
}
