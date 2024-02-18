package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	sst "github.com/picosh/pobj/storage"
)

type StorageFS struct {
	*sst.StorageFS
}

func NewStorageFS(dir string) (*StorageFS, error) {
	st, err := sst.NewStorageFS(dir)
	if err != nil {
		return nil, err
	}
	return &StorageFS{st}, nil
}

func (s *StorageFS) ServeObject(bucket sst.Bucket, fpath string, opts *ImgProcessOpts) (io.ReadCloser, string, error) {
	if opts == nil || os.Getenv("IMGPROXY_URL") == "" {
		contentType := GetMimeType(fpath)
		rc, _, _, err := s.GetObject(bucket, fpath)
		return rc, contentType, err
	}

	filePath := filepath.Join(bucket.Path, fpath)
	dataURL := fmt.Sprintf("local://%s", filePath)
	return HandleProxy(dataURL, opts)
}

func (s *StorageFS) GetObjectSize(bucket sst.Bucket, fpath string) (int64, error) {
	fi, err := os.Stat(filepath.Join(bucket.Path, fpath))
	if err != nil {
		return 0, err
	}
	size := fi.Size()
	return size, nil
}
