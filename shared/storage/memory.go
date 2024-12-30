package storage

import (
	"io"
	"net/http"

	sst "github.com/picosh/pobj/storage"
)

type StorageMemory struct {
	*sst.StorageMemory
}

func NewStorageMemory(sto map[string]map[string]string) (*StorageMemory, error) {
	st, err := sst.NewStorageMemory(sto)
	if err != nil {
		return nil, err
	}
	return &StorageMemory{st}, nil
}

func (s *StorageMemory) ServeObject(bucket sst.Bucket, fpath string, opts *ImgProcessOpts) (io.ReadCloser, *sst.ObjectInfo, error) {
	obj, info, err := s.GetObject(bucket, fpath)
	if info.Metadata == nil {
		info.Metadata = make(http.Header)
	}
	mimeType := GetMimeType(fpath)
	info.Metadata.Set("content-type", mimeType)
	return obj, info, err
}
