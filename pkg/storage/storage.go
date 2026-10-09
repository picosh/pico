package storage

import (
	"io"
	"os"
	"time"

	"github.com/picosh/pico/pkg/send/utils"
)

type Bucket struct {
	Name string
	Path string
	Root string
}

type ObjectInfo struct {
	Size         int64
	LastModified time.Time
	ETag         string
	ContentType  string
}

type BucketStorage interface {
	GetBucket(name string) (Bucket, error)
	GetBucketQuota(bucket Bucket) (uint64, error)
	UpsertBucket(name string) (Bucket, error)
	ListBuckets() ([]string, error)
	DeleteBucket(bucket Bucket) error
}

type ObjectStorage interface {
	GetObject(bucket Bucket, fpath string) (utils.ReadAndReaderAtCloser, *ObjectInfo, error)
	// StatObject returns an object's info without opening it.
	StatObject(bucket Bucket, fpath string) (*ObjectInfo, error)
	PutObject(bucket Bucket, fpath string, contents io.Reader, info *ObjectInfo) (string, int64, error)
	// PutDir creates a directory, and its parents, that is listed even
	// while it's empty.
	PutDir(bucket Bucket, dir string) error
	// DeleteObject removes a file or an empty directory. Deleting a missing
	// path succeeds. A directory stays after its last file is deleted.
	DeleteObject(bucket Bucket, fpath string) error
	ListObjects(bucket Bucket, dir string, recursive bool) ([]os.FileInfo, error)
}

type StorageServe interface {
	BucketStorage
	ObjectStorage
}
