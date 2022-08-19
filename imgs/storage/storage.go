package storage

import (
	"fmt"
	"os"
	"path"
)

type Bucket struct {
	Name string
	Path string
}

type ObjectStorage interface {
	GetBucket(name string) (Bucket, error)
	UpsertBucket(name string) (Bucket, error)

	GetFile(bucket Bucket, fname string) ([]byte, error)
	PutFile(bucket Bucket, fname string, contents []byte) (string, error)
	DeleteFile(bucket Bucket, fname string) error
}

type StorageFS struct {
	Dir string
}

func NewStorageFS(dir string) *StorageFS {
	return &StorageFS{Dir: dir}
}

// GetBucket - A bucket for the filesystem is just a directory.
func (s *StorageFS) GetBucket(name string) (Bucket, error) {
	dirPath := path.Join(s.Dir, name)
	bucket := Bucket{
		Name: name,
		Path: dirPath,
	}

	info, err := os.Stat(dirPath)
	if os.IsNotExist(err) {
		return bucket, fmt.Errorf("directory does not exist: %v %w\n", dirPath, err)
	}

	if err != nil {
		return bucket, fmt.Errorf("directory error: %v %w\n", dirPath, err)

	}

	if !info.IsDir() {
		return bucket, fmt.Errorf("directory is a file, not a directory: %#v\n", dirPath)
	}

	return bucket, nil
}

func (s *StorageFS) UpsertBucket(name string) (Bucket, error) {
	bucket, err := s.GetBucket(name)
	if err == nil {
		return bucket, nil
	}

	err = os.MkdirAll(bucket.Path, os.ModePerm)
	if err != nil {
		return bucket, err
	}

	return bucket, nil
}

func (s *StorageFS) GetFile(bucket Bucket, fname string) ([]byte, error) {
	dat, err := os.ReadFile(path.Join(bucket.Path, fname))
	if err != nil {
		return []byte{}, err
	}

	return dat, nil
}

func (s *StorageFS) PutFile(bucket Bucket, fname string, contents []byte) (string, error) {
	loc := path.Join(bucket.Path, fname)
	err := os.WriteFile(loc, contents, 0644)
	if err != nil {
		return "", err
	}

	return loc, nil
}

func (s *StorageFS) DeleteFile(bucket Bucket, fname string) error {
	loc := path.Join(bucket.Path, fname)
	err := os.Remove(loc)
	if err != nil {
		return err
	}

	return nil
}
