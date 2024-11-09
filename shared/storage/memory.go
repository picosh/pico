package storage

import (
	"io"

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

func (s *StorageMemory) ServeObject(bucket sst.Bucket, fpath string, opts *ImgProcessOpts) (io.ReadCloser, string, error) {
	obj, _, err := s.GetObject(bucket, fpath)
	return obj, GetMimeType(fpath), err
}
