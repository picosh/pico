package storage

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	sst "github.com/picosh/pico/pkg/pobj/storage"
	"github.com/picosh/pico/pkg/shared/mime"
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

func (s *StorageFS) ServeObject(r *http.Request, bucket sst.Bucket, fpath string, opts *ImgProcessOpts) (io.ReadCloser, *sst.ObjectInfo, error) {
	var rc io.ReadCloser
	info := &sst.ObjectInfo{}
	var err error
	mimeType := mime.GetMimeType(fpath)
	if !strings.HasPrefix(mimeType, "image/") || opts == nil || os.Getenv("IMGPROXY_URL") == "" {
		rc, info, err = s.GetObject(bucket, fpath)
		if info.Metadata == nil {
			info.Metadata = map[string][]string{}
		}
		// StorageFS never returns a content-type.
		info.Metadata.Set("content-type", mimeType)
	} else {
		filePath := filepath.Join(bucket.Name, fpath)
		dataURL := fmt.Sprintf("local:///%s", filePath)
		rc, info, err = HandleProxy(r, s.Logger, dataURL, opts)
	}
	if err != nil {
		return nil, nil, err
	}
	return rc, info, err
}
