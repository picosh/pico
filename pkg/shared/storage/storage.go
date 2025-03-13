package storage

import (
	"io"

	sst "github.com/picosh/pico/pkg/pobj/storage"
)

type StorageServe interface {
	sst.ObjectStorage
	ServeObject(bucket sst.Bucket, fpath string, opts *ImgProcessOpts) (io.ReadCloser, *sst.ObjectInfo, error)
}
