package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/picosh/pico/pkg/cache"
	"github.com/picosh/pico/pkg/send/utils"

	garage "git.deuxfleurs.fr/garage-sdk/garage-admin-sdk-golang"
)

type StorageGarage struct {
	Client      *minio.Client
	ClientKey   string
	Admin       *garage.APIClient
	AdminCtx    context.Context
	BucketCache *expirable.LRU[string, CachedBucket]
	Logger      *slog.Logger
}

var (
	_ ObjectStorage = &StorageGarage{}
	_ ObjectStorage = (*StorageGarage)(nil)
)

func cleanPath(path string) string {
	cleanPath := filepath.Clean(path)
	if cleanPath == "." {
		return ""
	}

	return cleanPath
}

func NewStorageGarage(logger *slog.Logger, address, user, pass, adminAddress, token string) (*StorageGarage, error) {
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

	adminEndpoint, err := url.Parse(adminAddress)
	if err != nil {
		return nil, err
	}

	configuration := garage.NewConfiguration()
	configuration.Host = adminEndpoint.Host
	configuration.Scheme = adminEndpoint.Scheme

	client := garage.NewAPIClient(configuration)
	ctx := context.WithValue(context.Background(), garage.ContextAccessToken, token)

	_, _, err = client.LayoutAPI.GetLayout(ctx).Execute()
	if err != nil {
		return nil, err
	}

	mini := &StorageGarage{
		Client:      mClient,
		ClientKey:   user,
		Admin:       client,
		AdminCtx:    ctx,
		BucketCache: expirable.NewLRU[string, CachedBucket](2048, nil, cache.CacheTimeout),
		Logger:      logger,
	}
	return mini, err
}

func (s *StorageGarage) GetBucket(name string) (Bucket, error) {
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

func (s *StorageGarage) UpsertBucket(name string) (Bucket, error) {
	bucket, err := s.GetBucket(name)
	if err == nil {
		return bucket, nil
	}

	createBucketRequest := garage.NewCreateBucketRequest()
	createBucketRequest.SetGlobalAlias(name)

	bucketInfo, _, err := s.Admin.BucketAPI.CreateBucket(s.AdminCtx).CreateBucketRequest(*createBucketRequest).Execute()
	if err != nil {
		return bucket, err
	}

	permissions := garage.NewAllowBucketKeyRequestPermissions(true, true, true)
	allowBucketKeyRequest := garage.NewAllowBucketKeyRequest(bucketInfo.GetId(), s.ClientKey, *permissions)

	_, _, err = s.Admin.BucketAPI.AllowBucketKey(s.AdminCtx).AllowBucketKeyRequest(*allowBucketKeyRequest).Execute()
	if err != nil {
		return bucket, err
	}

	// We can have garage enforce the quota on the bucket
	// bucketQuotas := garage.NewUpdateBucketRequestQuotas()
	// bucketQuotas.SetMaxSize()

	// updateBucketRequest := garage.NewUpdateBucketRequest()
	// updateBucketRequest.SetQuotas(*bucketQuotas)

	// _, _, err = s.Admin.BucketAPI.UpdateBucket(s.AdminCtx).Id(bucketInfo.GetId()).UpdateBucketRequest(*updateBucketRequest).Execute()
	// if err != nil {
	// 	return bucket, err
	// }

	s.BucketCache.Remove(name)

	return bucket, nil
}

func (s *StorageGarage) GetBucketQuota(bucket Bucket) (uint64, error) {
	info, _, err := s.Admin.BucketAPI.GetBucketInfo(s.AdminCtx).GlobalAlias(bucket.Name).Execute()
	if err != nil {
		return 0, err
	}

	if info == nil {
		return 0, fmt.Errorf("bucket %s not found", bucket.Name)
	}

	if info.Bytes == nil {
		return 0, fmt.Errorf("bucket %s has no size", bucket.Name)
	}

	return uint64(info.GetBytes()), nil
}

func (s *StorageGarage) ListBuckets() ([]string, error) {
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

func (s *StorageGarage) ListObjects(bucket Bucket, dir string, recursive bool) ([]os.FileInfo, error) {
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

func (s *StorageGarage) DeleteBucket(bucket Bucket) error {
	info, _, err := s.Admin.BucketAPI.GetBucketInfo(s.AdminCtx).GlobalAlias(bucket.Name).Execute()
	if err != nil {
		return err
	}

	_, err = s.Admin.BucketAPI.DeleteBucket(s.AdminCtx).Id(info.GetId()).Execute()
	if err != nil {
		return err
	}

	s.BucketCache.Remove(bucket.Name)
	return nil
}

func (s *StorageGarage) GetObject(bucket Bucket, fpath string) (utils.ReadAndReaderAtCloser, *ObjectInfo, error) {
	objInfo := &ObjectInfo{
		Size:         0,
		LastModified: time.Time{},
		ETag:         "",
	}

	info, err := s.Client.StatObject(context.Background(), bucket.Name, cleanPath(fpath), minio.StatObjectOptions{})
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

	obj, err := s.Client.GetObject(context.Background(), bucket.Name, cleanPath(fpath), minio.GetObjectOptions{})
	if err != nil {
		return nil, objInfo, err
	}

	return obj, objInfo, nil
}

func (s *StorageGarage) PutObject(bucket Bucket, fpath string, contents io.Reader, entry *utils.FileEntry) (string, int64, error) {
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

	info, err := s.Client.PutObject(context.TODO(), bucket.Name, cleanPath(fpath), contents, objSize, opts)
	if err == nil {
		return fmt.Sprintf("%s/%s", info.Bucket, info.Key), info.Size, nil
	}

	if err.Error() == "Bad request: Empty body" {
		r, err := s.PutEmptyObject(bucket, fpath, entry)
		return r, 0, err
	}

	return "", 0, err
}

func (s *StorageGarage) PutEmptyObject(bucket Bucket, fpath string, entry *utils.FileEntry) (string, error) {
	opts := minio.PutObjectOptions{
		UserMetadata: map[string]string{
			"Mtime": fmt.Sprint(time.Now().Unix()),
		},
	}

	if entry.Mtime > 0 {
		opts.UserMetadata["Mtime"] = fmt.Sprint(entry.Mtime)
	}

	info, err := s.Client.PutObject(context.TODO(), bucket.Name, cleanPath(fpath), nil, 0, opts)

	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/%s", info.Bucket, info.Key), nil
}

func (s *StorageGarage) DeleteObject(bucket Bucket, fpath string) error {
	err := s.Client.RemoveObject(context.TODO(), bucket.Name, cleanPath(fpath), minio.RemoveObjectOptions{})
	return err
}
