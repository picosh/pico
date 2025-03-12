package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/picosh/pico/pkg/send/utils"
)

type StorageMemory struct {
	storage map[string]map[string]string
	mu      sync.RWMutex
}

var _ ObjectStorage = &StorageMemory{}
var _ ObjectStorage = (*StorageMemory)(nil)

func NewStorageMemory(st map[string]map[string]string) (*StorageMemory, error) {
	return &StorageMemory{
		storage: st,
	}, nil
}

func (s *StorageMemory) GetBucket(name string) (Bucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	bucket := Bucket{
		Name: name,
		Path: name,
	}

	_, ok := s.storage[name]
	if !ok {
		return bucket, fmt.Errorf("bucket does not exist")
	}

	return bucket, nil
}

func (s *StorageMemory) UpsertBucket(name string) (Bucket, error) {
	bucket, err := s.GetBucket(name)
	if err == nil {
		return bucket, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.storage[name] = map[string]string{}
	return bucket, nil
}

func (s *StorageMemory) GetBucketQuota(bucket Bucket) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	objects := s.storage[bucket.Path]
	size := 0
	for _, val := range objects {
		size += len([]byte(val))
	}
	return uint64(size), nil
}

func (s *StorageMemory) DeleteBucket(bucket Bucket) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.storage, bucket.Path)
	return nil
}

func (s *StorageMemory) GetObject(bucket Bucket, fpath string) (utils.ReadAndReaderAtCloser, *ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !strings.HasPrefix(fpath, "/") {
		fpath = "/" + fpath
	}

	objInfo := &ObjectInfo{
		LastModified: time.Time{},
		Metadata:     nil,
		UserMetadata: map[string]string{},
	}

	dat, ok := s.storage[bucket.Path][fpath]
	if !ok {
		return nil, objInfo, fmt.Errorf("object does not exist: %s", fpath)
	}

	objInfo.Size = int64(len([]byte(dat)))
	reader := utils.NopReadAndReaderAtCloser(strings.NewReader(dat))
	return reader, objInfo, nil
}

func (s *StorageMemory) PutObject(bucket Bucket, fpath string, contents io.Reader, entry *utils.FileEntry) (string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, err := io.ReadAll(contents)
	if err != nil {
		return "", 0, err
	}

	s.storage[bucket.Path][fpath] = string(d)
	return fmt.Sprintf("%s%s", bucket.Path, fpath), int64(len(d)), nil
}

func (s *StorageMemory) DeleteObject(bucket Bucket, fpath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.storage[bucket.Path], fpath)
	return nil
}

func (s *StorageMemory) ListBuckets() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	buckets := []string{}
	for key := range s.storage {
		buckets = append(buckets, key)
	}
	return buckets, nil
}

func (s *StorageMemory) ListObjects(bucket Bucket, dir string, recursive bool) ([]os.FileInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var fileList []os.FileInfo

	resolved := dir

	if !strings.HasPrefix(resolved, "/") {
		resolved = "/" + resolved
	}

	objects := s.storage[bucket.Path]
	// dir is actually an object
	oval, ok := objects[resolved]
	if ok {
		fileList = append(fileList, &utils.VirtualFile{
			FName:    filepath.Base(resolved),
			FIsDir:   false,
			FSize:    int64(len([]byte(oval))),
			FModTime: time.Time{},
		})
		return fileList, nil
	}

	for key, val := range objects {
		if !strings.HasPrefix(key, resolved) {
			continue
		}

		rep := strings.Replace(key, resolved, "", 1)
		fdir := filepath.Dir(rep)
		fname := filepath.Base(rep)
		paths := strings.Split(fdir, "/")

		if fdir == "/" {
			ffname := filepath.Base(resolved)
			fileList = append(fileList, &utils.VirtualFile{
				FName:  ffname,
				FIsDir: true,
			})
		}

		for _, p := range paths {
			if p == "" || p == "/" || p == "." {
				continue
			}
			fileList = append(fileList, &utils.VirtualFile{
				FName:  p,
				FIsDir: true,
			})
		}

		trimRes := strings.TrimSuffix(resolved, "/")
		dirKey := filepath.Dir(key)
		if recursive {
			fileList = append(fileList, &utils.VirtualFile{
				FName:    fname,
				FIsDir:   false,
				FSize:    int64(len([]byte(val))),
				FModTime: time.Time{},
			})
		} else if resolved == dirKey || trimRes == dirKey {
			fileList = append(fileList, &utils.VirtualFile{
				FName:    fname,
				FIsDir:   false,
				FSize:    int64(len([]byte(val))),
				FModTime: time.Time{},
			})
		}
	}

	return fileList, nil
}
