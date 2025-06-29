package storage

import (
	"crypto/md5"
	"encoding/hex"
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

	"github.com/google/renameio/v2"
	"github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared/mime"
	putils "github.com/picosh/utils"
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
// This is different from minio impl which requires all files be deleted first.
func (s *StorageFS) DeleteBucket(bucket Bucket) error {
	return os.RemoveAll(bucket.Path)
}

func (s *StorageFS) GetObject(bucket Bucket, fpath string) (utils.ReadAndReaderAtCloser, *ObjectInfo, error) {
	objInfo := &ObjectInfo{
		Size:         0,
		LastModified: time.Time{},
		Metadata:     make(http.Header),
		ETag:         "",
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

	etag := ""
	// only generate etag if file is less than 10MB
	if info.Size() <= int64(10*putils.MB) {
		// calculate etag
		h := md5.New()
		if _, err := io.Copy(h, dat); err != nil {
			_ = dat.Close()
			return nil, objInfo, err
		}
		md5Sum := h.Sum(nil)
		etag = hex.EncodeToString(md5Sum)

		// reset os.File reader
		_, err = dat.Seek(0, io.SeekStart)
		if err != nil {
			_ = dat.Close()
			return nil, objInfo, err
		}
	}

	objInfo.ETag = etag
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
	out, err := renameio.NewPendingFile(loc)
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
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// traverse up the folder tree and remove all empty folders
	dir := filepath.Dir(loc)
	for dir != "" {
		f, err := os.Open(dir)
		if err != nil {
			s.Logger.Info("open dir", "dir", dir, "err", err)
			break
		}
		defer func() {
			_ = f.Close()
		}()

		// https://stackoverflow.com/a/30708914
		contents, err := f.Readdirnames(-1)
		if err != nil {
			s.Logger.Info("read dir", "dir", dir, "err", err)
			break
		}
		if len(contents) > 0 {
			break
		}

		err = os.Remove(dir)
		if err != nil {
			s.Logger.Info("remove dir", "dir", dir, "err", err)
			break
		}
		fp := strings.Split(dir, "/")
		prefix := ""
		if strings.HasPrefix(loc, "/") {
			prefix = "/"
		}
		dir = prefix + filepath.Join(fp[:len(fp)-1]...)
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
