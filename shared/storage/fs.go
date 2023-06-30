package storage

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/picosh/pico/wish/send/utils"
)

// https://stackoverflow.com/a/32482941
func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return err
	})

	return size, err
}

type StorageFS struct {
	Dir string
}

func NewStorageFS(dir string) (*StorageFS, error) {
	return &StorageFS{Dir: dir}, nil
}

func (s *StorageFS) GetBucket(name string) (Bucket, error) {
	dirPath := filepath.Join(s.Dir, name)
	bucket := Bucket{
		Name: name,
		Path: dirPath,
	}

	info, err := os.Stat(dirPath)
	if os.IsNotExist(err) {
		return bucket, fmt.Errorf("directory does not exist: %v %w", dirPath, err)
	}

	if err != nil {
		return bucket, fmt.Errorf("directory error: %v %w", dirPath, err)

	}

	if !info.IsDir() {
		return bucket, fmt.Errorf("directory is a file, not a directory: %#v", dirPath)
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

func (s *StorageFS) GetBucketQuota(bucket Bucket) (uint64, error) {
	dsize, err := dirSize(bucket.Path)
	return uint64(dsize), err
}

func (s *StorageFS) DeleteBucket(bucket Bucket) error {
	return os.RemoveAll(bucket.Path)
}

func (s *StorageFS) GetFile(bucket Bucket, fpath string) (ReaderAtCloser, error) {
	dat, err := os.Open(filepath.Join(bucket.Path, fpath))
	if err != nil {
		return nil, err
	}

	return dat, nil
}

func (s *StorageFS) PutFile(bucket Bucket, fpath string, contents ReaderAtCloser) (string, error) {
	loc := filepath.Join(bucket.Path, fpath)
	err := os.MkdirAll(filepath.Dir(loc), os.ModePerm)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(loc, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return "", err
	}
	defer f.Close()

	_, err = io.Copy(f, contents)
	if err != nil {
		return "", err
	}

	return loc, nil
}

func (s *StorageFS) DeleteFile(bucket Bucket, fpath string) error {
	loc := filepath.Join(bucket.Path, fpath)
	err := os.Remove(loc)
	if err != nil {
		return err
	}

	return nil
}

func (s *StorageFS) ListFiles(bucket Bucket, dir string) ([]os.FileInfo, error) {
	var fileList []os.FileInfo
	fpath := filepath.Join(bucket.Path, dir)
	err := filepath.WalkDir(fpath, func(s string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			fileInfo, err := os.Stat(s)
			if err != nil {
				return err
			}
			info := &utils.VirtualFile{
				FName:    strings.Replace(s, bucket.Path, "", 1),
				FIsDir:   d.IsDir(),
				FSize:    fileInfo.Size(),
				FModTime: fileInfo.ModTime(),
			}
			fileList = append(fileList, info)
		}
		return nil
	})

	return fileList, err
}
