package storage

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared/mime"
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
	Dir    string
	Logger *slog.Logger
}

var _ ObjectStorage = &StorageFS{}
var _ ObjectStorage = (*StorageFS)(nil)

func NewStorageFS(logger *slog.Logger, dir string) (*StorageFS, error) {
	return &StorageFS{Logger: logger, Dir: dir}, nil
}

func (s *StorageFS) GetBucket(name string) (Bucket, error) {
	dirPath := filepath.Join(s.Dir, name)
	bucket := Bucket{
		Name: name,
		Path: dirPath,
	}
	s.Logger.Info("get bucket", "dir", dirPath)

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
	s.Logger.Info("upsert bucket", "name", name)
	bucket, err := s.GetBucket(name)
	if err == nil {
		return bucket, nil
	}

	dir := filepath.Join(s.Dir, bucket.Path)
	s.Logger.Info("bucket not found, creating", "dir", dir, "err", err)
	err = os.MkdirAll(dir, os.ModePerm)
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

func (s *StorageFS) GetObject(bucket Bucket, fpath string) (utils.ReadAndReaderAtCloser, *ObjectInfo, error) {
	objInfo := &ObjectInfo{
		LastModified: time.Time{},
		Metadata:     make(http.Header),
	}

	dat, err := os.Open(filepath.Join(bucket.Path, fpath))
	if err != nil {
		return nil, objInfo, err
	}

	info, err := dat.Stat()
	if err != nil {
		return nil, objInfo, err
	}

	objInfo.Size = info.Size()
	objInfo.LastModified = info.ModTime()
	objInfo.Metadata.Set("content-type", mime.GetMimeType(fpath))
	return dat, objInfo, nil
}

func (s *StorageFS) PutObject(bucket Bucket, fpath string, contents io.Reader, entry *utils.FileEntry) (string, int64, error) {
	loc := filepath.Join(bucket.Path, fpath)
	err := os.MkdirAll(filepath.Dir(loc), os.ModePerm)
	if err != nil {
		return "", 0, err
	}
	f, err := os.OpenFile(loc, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return "", 0, err
	}

	size, err := io.Copy(f, contents)
	if err != nil {
		return "", 0, err
	}

	f.Close()

	if entry.Mtime > 0 {
		uTime := time.Unix(entry.Mtime, 0)
		_ = os.Chtimes(loc, uTime, uTime)
	}

	return loc, size, nil
}

func (s *StorageFS) DeleteObject(bucket Bucket, fpath string) error {
	loc := filepath.Join(bucket.Path, fpath)
	err := os.Remove(loc)
	if err != nil {
		return err
	}

	// try to remove dir if it is empty
	dir := filepath.Dir(loc)
	_ = os.Remove(dir)

	return nil
}

func (s *StorageFS) ListBuckets() ([]string, error) {
	return []string{}, fmt.Errorf("not implemented")
}

func (s *StorageFS) ListObjects(bucket Bucket, dir string, recursive bool) ([]os.FileInfo, error) {
	var fileList []os.FileInfo

	fpath := path.Join(bucket.Path, dir)

	info, err := os.Stat(fpath)
	if err != nil {
		return fileList, err
	}

	if info.IsDir() && !strings.HasSuffix(dir, "/") {
		fileList = append(fileList, &utils.VirtualFile{
			FName:    "",
			FIsDir:   info.IsDir(),
			FSize:    info.Size(),
			FModTime: info.ModTime(),
		})

		return fileList, err
	}

	var files []fs.DirEntry

	if recursive {
		err = filepath.WalkDir(fpath, func(s string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			files = append(files, d)
			return nil
		})
		if err != nil {
			fileList = append(fileList, info)
			return fileList, nil
		}
	} else {
		files, err = os.ReadDir(fpath)
		if err != nil {
			fileList = append(fileList, info)
			return fileList, nil
		}
	}

	for _, f := range files {
		info, err := f.Info()
		if err != nil {
			return fileList, err
		}

		i := &utils.VirtualFile{
			FName:    f.Name(),
			FIsDir:   f.IsDir(),
			FSize:    info.Size(),
			FModTime: info.ModTime(),
		}

		fileList = append(fileList, i)
	}

	return fileList, err
}
