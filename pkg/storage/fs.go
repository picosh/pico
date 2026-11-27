package storage

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/renameio/v2"
	"github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared/mime"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

var KB = 1000
var MB = KB * 1000

// walkSlots caps the extra goroutines reading directories and lstatting
// entries across every walk in the process. On a network filesystem each of
// those waits on a round trip, so a serial walk is slow, but concurrent
// uploads must not multiply the load.
var walkSlots = semaphore.NewWeighted(32)

// walkChunk is how many entries one worker lstats at a time, so a large
// directory is spread across workers too.
const walkChunk = 64

// slotGroup runs work on a free slot, or inline when every slot is taken, so
// each walk still makes progress and never waits on another walk's slots.
type slotGroup struct {
	g     *errgroup.Group
	ctx   context.Context
	slots *semaphore.Weighted
}

func newSlotGroup(slots *semaphore.Weighted) *slotGroup {
	g, ctx := errgroup.WithContext(context.Background())
	return &slotGroup{g: g, ctx: ctx, slots: slots}
}

func (s *slotGroup) Go(f func() error) error {
	if !s.slots.TryAcquire(1) {
		return f()
	}
	s.g.Go(func() error {
		defer s.slots.Release(1)
		return f()
	})
	return nil
}

func (s *slotGroup) Wait() error { return s.g.Wait() }

// walkTree calls visit for every entry below the directory root with its
// slash-separated path relative to root, concurrently and in no order.
// Directories are descended without following symlinks. The first error from
// reading a directory or from visit stops the walk.
func walkTree(root string, slots *semaphore.Weighted, visit func(rel string, d fs.DirEntry) error) error {
	sg := newSlotGroup(slots)

	visitAll := func(dir string, entries []fs.DirEntry) error {
		for _, d := range entries {
			if err := sg.ctx.Err(); err != nil {
				return err
			}
			if err := visit(path.Join(dir, d.Name()), d); err != nil {
				return err
			}
		}
		return nil
	}

	var walk func(rel string) error
	walk = func(rel string) error {
		if err := sg.ctx.Err(); err != nil {
			return err
		}
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		for _, d := range entries {
			if d.IsDir() {
				sub := path.Join(rel, d.Name())
				if err := sg.Go(func() error { return walk(sub) }); err != nil {
					return err
				}
			}
		}
		for chunk := range slices.Chunk(entries, walkChunk) {
			if err := sg.Go(func() error { return visitAll(rel, chunk) }); err != nil {
				return err
			}
		}
		return nil
	}

	sg.g.Go(func() error { return walk("") })
	return sg.Wait()
}

// dirSize sums the lstat sizes of everything under root except directories,
// without following symlinks.
func dirSize(root string) (int64, error) {
	return walkSize(root, walkSlots)
}

func walkSize(root string, slots *semaphore.Weighted) (int64, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return info.Size(), nil
	}

	var size atomic.Int64
	err = walkTree(root, slots, func(_ string, d fs.DirEntry) error {
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size.Add(info.Size())
		return nil
	})
	return size.Load(), err
}

// listTree lists everything below root in filepath.WalkDir order, leaving out
// entries that can't be lstatted. A root that isn't a directory, a symlink
// included, has nothing below it.
func listTree(root string, slots *semaphore.Weighted) ([]utils.VirtualFile, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}

	var mu sync.Mutex
	var files []utils.VirtualFile
	err = walkTree(root, slots, func(rel string, d fs.DirEntry) error {
		if d.Name() == legacyKeepDir {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		files = append(files, utils.VirtualFile{
			FName:    rel,
			FIsDir:   info.IsDir(),
			FSize:    info.Size(),
			FModTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b utils.VirtualFile) int {
		return compareWalkOrder(a.FName, b.FName)
	})
	return files, nil
}

// compareWalkOrder orders slash-separated paths the way filepath.WalkDir
// visits them: a directory before its contents, names sorted within each.
func compareWalkOrder(a, b string) int {
	for {
		aseg, arest, amore := strings.Cut(a, "/")
		bseg, brest, bmore := strings.Cut(b, "/")
		if c := strings.Compare(aseg, bseg); c != 0 {
			return c
		}
		switch {
		case !amore && !bmore:
			return 0
		case !amore:
			return -1
		case !bmore:
			return 1
		}
		a, b = arest, brest
	}
}

// listEntries lstats entries across slots, keeping their order and leaving
// out the ones that can't be lstatted.
func listEntries(entries []fs.DirEntry, slots *semaphore.Weighted) []utils.VirtualFile {
	infos := make([]fs.FileInfo, len(entries))
	sg := newSlotGroup(slots)
	for lo := 0; lo < len(entries); lo += walkChunk {
		hi := min(lo+walkChunk, len(entries))
		_ = sg.Go(func() error {
			for i := lo; i < hi; i++ {
				if entries[i].Name() == legacyKeepDir {
					continue
				}
				if info, err := entries[i].Info(); err == nil {
					infos[i] = info
				}
			}
			return nil
		})
	}
	_ = sg.Wait()

	var files []utils.VirtualFile
	for _, info := range infos {
		if info == nil {
			continue
		}
		files = append(files, utils.VirtualFile{
			FName:    info.Name(),
			FIsDir:   info.IsDir(),
			FSize:    info.Size(),
			FModTime: info.ModTime(),
		})
	}
	return files
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

// objectPath is where fpath lives in bucket. fpath is rooted before it is
// cleaned, so ".." can't climb out of the bucket.
func objectPath(bucket Bucket, fpath string) string {
	return filepath.Join(bucket.Path, filepath.FromSlash(path.Clean("/"+fpath)))
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

	dat, err := os.Open(objectPath(bucket, fpath))
	if err != nil {
		return nil, objInfo, err
	}

	info, err := dat.Stat()
	if err != nil {
		_ = dat.Close()
		return nil, objInfo, err
	}
	if info.IsDir() {
		_ = dat.Close()
		return nil, objInfo, fmt.Errorf("%s is a directory: %w", fpath, fs.ErrNotExist)
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
	info, err := os.Stat(objectPath(bucket, fpath))
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
	loc := objectPath(bucket, fpath)
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
	return os.MkdirAll(objectPath(bucket, dir), os.ModePerm)
}

func (s *StorageFS) DeleteObject(bucket Bucket, fpath string) error {
	loc := objectPath(bucket, fpath)
	if loc == filepath.Clean(bucket.Path) {
		return nil
	}
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

	fpath := objectPath(bucket, dir)

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
		files, err = listTree(fpath, walkSlots)
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
		files = listEntries(fls, walkSlots)
	}

	for _, f := range files {
		fileList = append(fileList, &f)
	}

	return fileList, err
}
