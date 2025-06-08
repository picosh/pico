package storage

import (
	"io"
	"net/http"

	sst "github.com/picosh/pico/pkg/pobj/storage"
)

type StorageServe interface {
	sst.ObjectStorage
	ServeObject(r *http.Request, bucket sst.Bucket, fpath string, opts *ImgProcessOpts) (io.ReadCloser, *sst.ObjectInfo, error)
}
