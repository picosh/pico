package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/minio/madmin-go/v3"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/picosh/pico/pkg/cache"
	"github.com/picosh/pico/pkg/send/utils"
)

type StorageMinio struct {
	Client      *minio.Client
	Admin       *madmin.AdminClient
	BucketCache *expirable.LRU[string, CachedBucket]
	Logger      *slog.Logger
}

type CachedBucket struct {
	Bucket
	Error error
}

type CachedObjectInfo struct {
	*ObjectInfo
	Error error
}

var (
	_ ObjectStorage = &StorageMinio{}
	_ ObjectStorage = (*StorageMinio)(nil)
)

func NewStorageMinio(logger *slog.Logger, address, user, pass string) (*StorageMinio, error) {
	endpoint, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	ssl := endpoint.Scheme == "https"

	mClient, err := minio.New(endpoint.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(user, pass, ""),
		Secure: ssl,
	})
	if err != nil {
		return nil, err
	}

	aClient, err := madmin.NewWithOptions(
		endpoint.Host,
		&madmin.Options{
			Creds:  credentials.NewStaticV4(user, pass, ""),
			Secure: ssl,
		},
	)
	if err != nil {
		return nil, err
	}

	mini := &StorageMinio{
		Client:      mClient,
		Admin:       aClient,
		BucketCache: expirable.NewLRU[string, CachedBucket](2048, nil, cache.CacheTimeout),
		Logger:      logger,
	}
	return mini, err
}

func (s *StorageMinio) GetBucket(name string) (Bucket, error) {
	if cachedBucket, found := s.BucketCache.Get(name); found {
		s.Logger.Info("bucket found in lru cache", "name", name)
		return cachedBucket.Bucket, cachedBucket.Error
	}

	s.Logger.Info("bucket not found in lru cache", "name", name)

	bucket := Bucket{
		Name: name,
	}

	exists, err := s.Client.BucketExists(context.TODO(), bucket.Name)
	if err != nil || !exists {
		if err == nil {
			err = errors.New("bucket does not exist")
		}

		s.BucketCache.Add(name, CachedBucket{bucket, err})
		return bucket, err
	}

	s.BucketCache.Add(name, CachedBucket{bucket, nil})

	return bucket, nil
}

func (s *StorageMinio) UpsertBucket(name string) (Bucket, error) {
	bucket, err := s.GetBucket(name)
	if err == nil {
		return bucket, nil
	}

	err = s.Client.MakeBucket(context.TODO(), name, minio.MakeBucketOptions{})
	if err != nil {
		return bucket, err
	}

	s.BucketCache.Remove(name)

	return bucket, nil
}

func (s *StorageMinio) GetBucketQuota(bucket Bucket) (uint64, error) {
	info, err := s.Admin.AccountInfo(context.TODO(), madmin.AccountOpts{})
	if err != nil {
		return 0, nil
	}
	for _, b := range info.Buckets {
		if b.Name == bucket.Name {
			return b.Size, nil
		}
	}

	return 0, fmt.Errorf("%s bucket not found in account info", bucket.Name)
}

func (s *StorageMinio) ListBuckets() ([]string, error) {
	bcks := []string{}
	buckets, err := s.Client.ListBuckets(context.Background())
	if err != nil {
		return bcks, err
	}
	for _, bucket := range buckets {
		bcks = append(bcks, bucket.Name)
	}

	return bcks, nil
}

func (s *StorageMinio) ListObjects(bucket Bucket, dir string, recursive bool) ([]os.FileInfo, error) {
	var fileList []os.FileInfo

	resolved := strings.TrimPrefix(dir, "/")

	opts := minio.ListObjectsOptions{Prefix: resolved, Recursive: recursive, WithMetadata: true}
	for obj := range s.Client.ListObjects(context.Background(), bucket.Name, opts) {
		if obj.Err != nil {
			return fileList, obj.Err
		}

		isDir := strings.HasSuffix(obj.Key, string(os.PathSeparator))

		modTime := obj.LastModified

		if mtime, ok := obj.UserMetadata["Mtime"]; ok {
			mtimeUnix, err := strconv.Atoi(mtime)
			if err == nil {
				modTime = time.Unix(int64(mtimeUnix), 0)
			}
		}

		info := &utils.VirtualFile{
			FName:    strings.TrimSuffix(strings.TrimPrefix(obj.Key, resolved), "/"),
			FIsDir:   isDir,
			FSize:    obj.Size,
			FModTime: modTime,
		}
		fileList = append(fileList, info)
	}

	return fileList, nil
}

func (s *StorageMinio) DeleteBucket(bucket Bucket) error {
	return s.Client.RemoveBucket(context.TODO(), bucket.Name)
}

func (s *StorageMinio) GetObject(bucket Bucket, fpath string) (utils.ReadAndReaderAtCloser, *ObjectInfo, error) {
	objInfo := &ObjectInfo{
		Size:         0,
		LastModified: time.Time{},
		ETag:         "",
	}

	info, err := s.Client.StatObject(context.Background(), bucket.Name, fpath, minio.StatObjectOptions{})
	if err != nil {
		return nil, objInfo, err
	}

	objInfo.LastModified = info.LastModified
	objInfo.ETag = info.ETag
	objInfo.Metadata = info.Metadata
	objInfo.Size = info.Size

	if mtime, ok := info.UserMetadata["Mtime"]; ok {
		mtimeUnix, err := strconv.Atoi(mtime)
		if err == nil {
			objInfo.LastModified = time.Unix(int64(mtimeUnix), 0)
		}
	}

	obj, err := s.Client.GetObject(context.Background(), bucket.Name, fpath, minio.GetObjectOptions{})
	if err != nil {
		return nil, objInfo, err
	}

	return obj, objInfo, nil
}

func (s *StorageMinio) PutObject(bucket Bucket, fpath string, contents io.Reader, entry *utils.FileEntry) (string, int64, error) {
	opts := minio.PutObjectOptions{
		UserMetadata: map[string]string{
			"Mtime": fmt.Sprint(time.Now().Unix()),
		},
	}

	if entry.Mtime > 0 {
		opts.UserMetadata["Mtime"] = fmt.Sprint(entry.Mtime)
	}

	var objSize int64 = -1
	if entry.Size > 0 {
		objSize = entry.Size
	}
	info, err := s.Client.PutObject(context.TODO(), bucket.Name, fpath, contents, objSize, opts)

	if err != nil {
		return "", 0, err
	}

	return fmt.Sprintf("%s/%s", info.Bucket, info.Key), info.Size, nil
}

func (s *StorageMinio) PutEmptyObject(bucket Bucket, fpath string, entry *utils.FileEntry) (string, error) {
	opts := minio.PutObjectOptions{
		UserMetadata: map[string]string{
			"Mtime": fmt.Sprint(time.Now().Unix()),
		},
	}

	if entry.Mtime > 0 {
		opts.UserMetadata["Mtime"] = fmt.Sprint(entry.Mtime)
	}

	info, err := s.Client.PutObject(context.TODO(), bucket.Name, fpath, nil, 0, opts)

	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/%s", info.Bucket, info.Key), nil
}

func (s *StorageMinio) DeleteObject(bucket Bucket, fpath string) error {
	err := s.Client.RemoveObject(context.TODO(), bucket.Name, fpath, minio.RemoveObjectOptions{})
	return err
}
