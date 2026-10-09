package storage

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/picosh/pico/pkg/send/utils"
)

type seekableReader struct {
	*bytes.Reader
}

func (s *seekableReader) Close() error { return nil }

type StorageMemory struct {
	storage map[string]map[string]string
	mtimes  map[string]map[string]time.Time
	mu      sync.RWMutex
}

var _ StorageServe = &StorageMemory{}
var _ StorageServe = (*StorageMemory)(nil)

func NewStorageMemory(st map[string]map[string]string) (*StorageMemory, error) {
	now := time.Now()
	mtimes := map[string]map[string]time.Time{}
	for bucket, objects := range st {
		mtimes[bucket] = map[string]time.Time{}
		for key := range objects {
			mtimes[bucket][key] = now
		}
	}
	return &StorageMemory{
		storage: st,
		mtimes:  mtimes,
	}, nil
}

func memoryKey(fpath string) string {
	if !strings.HasPrefix(fpath, "/") {
		return "/" + fpath
	}
	return fpath
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
	s.mtimes[name] = map[string]time.Time{}
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
	delete(s.mtimes, bucket.Path)
	return nil
}

func (s *StorageMemory) GetObject(bucket Bucket, fpath string) (utils.ReadAndReaderAtCloser, *ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	fpath = memoryKey(fpath)
	objInfo := &ObjectInfo{}

	dat, ok := s.storage[bucket.Path][fpath]
	if !ok {
		return nil, objInfo, fmt.Errorf("object does not exist: %s", fpath)
	}

	objInfo.Size = int64(len([]byte(dat)))
	objInfo.LastModified = s.mtimes[bucket.Path][fpath]
	return &seekableReader{bytes.NewReader([]byte(dat))}, objInfo, nil
}

func (s *StorageMemory) StatObject(bucket Bucket, fpath string) (*ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	fpath = memoryKey(fpath)
	dat, ok := s.storage[bucket.Path][fpath]
	if !ok {
		return nil, fmt.Errorf("object does not exist: %s: %w", fpath, fs.ErrNotExist)
	}
	return &ObjectInfo{
		Size:         int64(len(dat)),
		LastModified: s.mtimes[bucket.Path][fpath],
	}, nil
}

func (s *StorageMemory) PutObject(bucket Bucket, fpath string, contents io.Reader, info *ObjectInfo) (string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, err := io.ReadAll(contents)
	if err != nil {
		return "", 0, err
	}

	fpath = memoryKey(fpath)
	mtime := info.LastModified
	if mtime.IsZero() {
		mtime = time.Now()
	}
	s.storage[bucket.Path][fpath] = string(d)
	if s.mtimes[bucket.Path] == nil {
		s.mtimes[bucket.Path] = map[string]time.Time{}
	}
	s.mtimes[bucket.Path][fpath] = mtime
	return fmt.Sprintf("%s%s", bucket.Path, fpath), int64(len(d)), nil
}

func (s *StorageMemory) DeleteObject(bucket Bucket, fpath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	fpath = memoryKey(fpath)
	delete(s.storage[bucket.Path], fpath)
	delete(s.mtimes[bucket.Path], fpath)
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

// ListObjects follows StorageFS: a path naming an object lists that object,
// a directory path without a trailing slash lists only the directory itself,
// and a path ending in a slash lists its contents relative to it.
func (s *StorageMemory) ListObjects(bucket Bucket, dir string, recursive bool) ([]os.FileInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	fileList := []os.FileInfo{}
	resolved := memoryKey(dir)
	objects := s.storage[bucket.Path]
	mtimes := s.mtimes[bucket.Path]

	if val, ok := objects[resolved]; ok {
		fileList = append(fileList, &utils.VirtualFile{
			FName:    filepath.Base(resolved),
			FSize:    int64(len(val)),
			FModTime: mtimes[resolved],
		})
		return fileList, nil
	}

	if !strings.HasSuffix(resolved, "/") {
		for key := range objects {
			if strings.HasPrefix(key, resolved+"/") {
				fileList = append(fileList, &utils.VirtualFile{FName: "", FIsDir: true})
				break
			}
		}
		return fileList, nil
	}

	seen := map[string]bool{}
	addDir := func(name string) {
		if !seen[name] {
			seen[name] = true
			fileList = append(fileList, &utils.VirtualFile{FName: name, FIsDir: true})
		}
	}
	for key, val := range objects {
		rel, ok := strings.CutPrefix(key, resolved)
		if !ok || rel == "" {
			continue
		}
		if !recursive {
			if d, _, nested := strings.Cut(rel, "/"); nested {
				addDir(d)
				continue
			}
		}
		for d := filepath.Dir(rel); d != "." && !seen[d]; d = filepath.Dir(d) {
			addDir(d)
		}
		fileList = append(fileList, &utils.VirtualFile{
			FName:    rel,
			FSize:    int64(len(val)),
			FModTime: mtimes[key],
		})
	}

	return fileList, nil
}
