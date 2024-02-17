package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	sst "github.com/picosh/pobj/storage"
)

type StorageMinio struct {
	*sst.StorageMinio
}

func NewStorageMinio(address, user, pass string) (*StorageMinio, error) {
	st, err := sst.NewStorageMinio(address, user, pass)
	if err != nil {
		return nil, err
	}
	return &StorageMinio{st}, nil
}

func (s *StorageMinio) ServeObject(bucket sst.Bucket, fpath string, opts *ImgProcessOpts) (io.ReadCloser, string, error) {
	if opts == nil || os.Getenv("IMGPROXY_URL") == "" {
		contentType := GetMimeType(fpath)
		rc, _, _, err := s.GetObject(bucket, fpath)
		return rc, contentType, err
	}

	filePath := filepath.Join(bucket.Name, fpath)
	dataURL := fmt.Sprintf("s3://%s", filePath)
	return HandleProxy(dataURL, opts)
}
