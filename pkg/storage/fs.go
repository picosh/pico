package storage

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/renameio/v2"
	"github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared/mime"
)

var KB = 1000
var MB = KB * 1000

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

// legacyKeepDir is the marker pgs used to write to keep empty directories.
// Listings hide it until cmd/scripts/clean-keep-dirs has removed them all.
const legacyKeepDir = "._pico_keep_dir"

type StorageFS struct {
	Dir    string
	Logger *slog.Logger
}

var _ StorageServe = &StorageFS{}
var _ StorageServe = (*StorageFS)(nil)

func NewStorageFS(logger *slog.Logger, dir string) (*StorageFS, error) {
	return &StorageFS{Logger: logger, Dir: dir}, nil
}

func (s *StorageFS) GetBucket(name string) (Bucket, error) {
	dirPath := filepath.Join(s.Dir, name)
	bucket := Bucket{
		Name: name,
		Path: dirPath,
	}
	// s.Logger.Info("get bucket", "dir", dirPath)

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

	dir := filepath.Join(s.Dir, name)
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

// DeleteBucket will delete all contents regardless if files exist inside of it.
func (s *StorageFS) DeleteBucket(bucket Bucket) error {
	return os.RemoveAll(bucket.Path)
}

func (s *StorageFS) GetObject(bucket Bucket, fpath string) (utils.ReadAndReaderAtCloser, *ObjectInfo, error) {
	contentType := mime.GetMimeType(fpath)
	objInfo := &ObjectInfo{
		Size:         0,
		LastModified: time.Time{},
		ETag:         "",
		ContentType:  contentType,
	}

	dat, err := os.Open(filepath.Join(bucket.Path, fpath))
	if err != nil {
		return nil, objInfo, err
	}

	info, err := dat.Stat()
	if err != nil {
		_ = dat.Close()
		return nil, objInfo, err
	}
	fillObjectInfo(objInfo, info)
	return dat, objInfo, nil
}

// fillObjectInfo sets an object's size, mtime and ETag from its file. The
// ETag is built from size and mtime, as web servers do, so serving a file
// doesn't mean reading it twice.
func fillObjectInfo(objInfo *ObjectInfo, info os.FileInfo) {
	objInfo.Size = info.Size()
	objInfo.LastModified = info.ModTime()
	objInfo.ETag = fmt.Sprintf("%x-%x", info.ModTime().UnixNano(), info.Size())
}

func (s *StorageFS) StatObject(bucket Bucket, fpath string) (*ObjectInfo, error) {
	info, err := os.Stat(filepath.Join(bucket.Path, fpath))
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory: %w", fpath, fs.ErrNotExist)
	}
	objInfo := &ObjectInfo{ContentType: mime.GetMimeType(fpath)}
	fillObjectInfo(objInfo, info)
	return objInfo, nil
}

func (s *StorageFS) PutObject(bucket Bucket, fpath string, contents io.Reader, info *ObjectInfo) (string, int64, error) {
	loc := filepath.Join(bucket.Path, fpath)
	err := os.MkdirAll(filepath.Dir(loc), os.ModePerm)
	if err != nil {
		return "", 0, err
	}
	out, err := renameio.NewPendingFile(loc, renameio.WithPermissions(os.ModePerm))
	if err != nil {
		return "", 0, err
	}

	size, err := io.Copy(out, contents)
	if err != nil {
		return "", 0, err
	}

	if err := out.CloseAtomicallyReplace(); err != nil {
		return "", 0, err
	}

	if !info.LastModified.IsZero() {
		uTime := info.LastModified
		_ = os.Chtimes(loc, uTime, uTime)
	}

	return loc, size, nil
}

func (s *StorageFS) PutDir(bucket Bucket, dir string) error {
	return os.MkdirAll(filepath.Join(bucket.Path, dir), os.ModePerm)
}

func (s *StorageFS) DeleteObject(bucket Bucket, fpath string) error {
	loc := filepath.Join(bucket.Path, fpath)
	// A leftover marker would keep an otherwise empty directory in place.
	_ = os.Remove(filepath.Join(loc, legacyKeepDir))
	err := os.Remove(loc)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *StorageFS) ListBuckets() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return []string{}, err
	}

	buckets := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		buckets = append(buckets, e.Name())
	}
	return buckets, nil
}

func (s *StorageFS) ListObjects(bucket Bucket, dir string, recursive bool) ([]os.FileInfo, error) {
	fileList := []os.FileInfo{}

	fpath := path.Join(bucket.Path, dir)

	info, err := os.Stat(fpath)
	if err != nil {
		if os.IsNotExist(err) {
			return fileList, nil
		}
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

	var files []utils.VirtualFile

	if recursive {
		err = filepath.WalkDir(fpath, func(s string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Name() == legacyKeepDir {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			fname := strings.TrimPrefix(s, fpath)
			if fname == "" {
				return nil
			}
			// rsync does not expect prefixed `/` so without this `rsync --delete` is borked
			fname = strings.TrimPrefix(fname, "/")
			files = append(files, utils.VirtualFile{
				FName:    fname,
				FIsDir:   info.IsDir(),
				FSize:    info.Size(),
				FModTime: info.ModTime(),
			})
			return nil
		})
		if err != nil {
			fileList = append(fileList, info)
			return fileList, nil
		}
	} else {
		fls, err := os.ReadDir(fpath)
		if err != nil {
			fileList = append(fileList, info)
			return fileList, nil
		}
		for _, d := range fls {
			if d.Name() == legacyKeepDir {
				continue
			}
			info, err := d.Info()
			if err != nil {
				continue
			}
			fp := info.Name()
			files = append(files, utils.VirtualFile{
				FName:    fp,
				FIsDir:   info.IsDir(),
				FSize:    info.Size(),
				FModTime: info.ModTime(),
			})
		}
	}

	for _, f := range files {
		fileList = append(fileList, &f)
	}

	return fileList, err
}
