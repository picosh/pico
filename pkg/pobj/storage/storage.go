package storage

import (
	"io"
	"net/http"
	"os"
	"time"

	"github.com/picosh/pico/pkg/send/utils"
)

type Bucket struct {
	Name string
	Path string
	Root string
}

type ObjectStorage interface {
	GetBucket(name string) (Bucket, error)
	GetBucketQuota(bucket Bucket) (uint64, error)
	UpsertBucket(name string) (Bucket, error)
	ListBuckets() ([]string, error)
	DeleteBucket(bucket Bucket) error

	GetObject(bucket Bucket, fpath string) (utils.ReadAndReaderAtCloser, *ObjectInfo, error)
	PutObject(bucket Bucket, fpath string, contents io.Reader, entry *utils.FileEntry) (string, int64, error)
	PutEmptyObject(bucket Bucket, fpath string, entry *utils.FileEntry) (string, error)

	DeleteObject(bucket Bucket, fpath string) error
	ListObjects(bucket Bucket, dir string, recursive bool) ([]os.FileInfo, error)
}

type ObjectInfo struct {
	Size         int64
	LastModified time.Time
	ETag         string
	Metadata     http.Header
}
