package storage

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	sst "github.com/picosh/pico/pkg/pobj/storage"
	"github.com/picosh/pico/pkg/shared/mime"
)

type StorageGarage struct {
	*sst.StorageGarage
}

func NewStorageGarage(logger *slog.Logger, address, user, pass, adminAddress, token string) (*StorageGarage, error) {
	st, err := sst.NewStorageGarage(logger, address, user, pass, adminAddress, token)
	if err != nil {
		return nil, err
	}
	return &StorageGarage{st}, nil
}

func (s *StorageGarage) ServeObject(bucket sst.Bucket, fpath string, opts *ImgProcessOpts) (io.ReadCloser, *sst.ObjectInfo, error) {
	var rc io.ReadCloser
	info := &sst.ObjectInfo{}
	var err error
	mimeType := mime.GetMimeType(fpath)
	if !strings.HasPrefix(mimeType, "image/") || opts == nil || os.Getenv("IMGPROXY_URL") == "" {
		rc, info, err = s.GetObject(bucket, fpath)
		if info.Metadata == nil {
			info.Metadata = map[string][]string{}
		}
		// Minio always returns application/octet-stream which needs to be overridden.
		info.Metadata.Set("content-type", mimeType)
	} else {
		filePath := filepath.Join(bucket.Name, fpath)
		dataURL := fmt.Sprintf("s3://%s", filePath)
		rc, info, err = HandleProxy(s.Logger, dataURL, opts)
	}
	if err != nil {
		return nil, nil, err
	}
	return rc, info, err
}
