package storage

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	sst "github.com/picosh/pobj/storage"
)

type StorageFS struct {
	*sst.StorageFS
	Logger *slog.Logger
}

func NewStorageFS(logger *slog.Logger, dir string) (*StorageFS, error) {
	st, err := sst.NewStorageFS(logger, dir)
	if err != nil {
		return nil, err
	}
	return &StorageFS{st, logger}, nil
}

func (s *StorageFS) ServeObject(bucket sst.Bucket, fpath string, opts *ImgProcessOpts) (io.ReadCloser, *sst.ObjectInfo, error) {
	var rc io.ReadCloser
	info := &sst.ObjectInfo{}
	var err error
	mimeType := GetMimeType(fpath)
	if !strings.HasPrefix(mimeType, "image/") || opts == nil || os.Getenv("IMGPROXY_URL") == "" {
		rc, info, err = s.GetObject(bucket, fpath)
		if info.Metadata == nil {
			info.Metadata = map[string][]string{}
		}
		// StorageFS never returns a content-type.
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
