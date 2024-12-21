package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

func (s *StorageMinio) ServeObject(bucket sst.Bucket, fpath string, opts *ImgProcessOpts) (io.ReadCloser, *sst.ObjectInfo, error) {
	var rc io.ReadCloser
	var info *sst.ObjectInfo
	var err error
	mimeType := GetMimeType(fpath)
	if !strings.HasPrefix(mimeType, "image/") || opts == nil || os.Getenv("IMGPROXY_URL") == "" {
		rc, info, err = s.GetObject(bucket, fpath)
		// Minio always returns application/octet-stream which needs to be overridden.
		info.Metadata.Set("content-type", mimeType)
	} else {
		filePath := filepath.Join(bucket.Name, fpath)
		dataURL := fmt.Sprintf("s3://%s", filePath)
		rc, info, err = HandleProxy(dataURL, opts)
	}
	if err != nil {
		return nil, nil, err
	}
	return rc, info, err
}
