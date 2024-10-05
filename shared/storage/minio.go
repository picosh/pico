package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/minio/minio-go/v7"
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
		rc, _, err := s.GetObject(bucket, fpath)
		return rc, contentType, err
	}

	filePath := filepath.Join(bucket.Name, fpath)
	dataURL := fmt.Sprintf("s3://%s", filePath)
	return HandleProxy(dataURL, opts)
}

func (s *StorageMinio) GetObjectSize(bucket sst.Bucket, fpath string) (int64, error) {
	info, err := s.Client.StatObject(context.Background(), bucket.Name, fpath, minio.StatObjectOptions{})
	if err != nil {
		return 0, err
	}
	return info.Size, nil
}
