package storage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"

	"github.com/minio/madmin-go/v3"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/picosh/pico/wish/send/utils"
)

type StorageMinio struct {
	Client *minio.Client
	Admin  *madmin.AdminClient
}

func NewStorageMinio(address, user, pass string) (*StorageMinio, error) {
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

	aClient, err := madmin.New(
		endpoint.Host,
		user,
		pass,
		ssl,
	)
	if err != nil {
		return nil, err
	}

	mini := &StorageMinio{
		Client: mClient,
		Admin:  aClient,
	}
	return mini, err
}

func (s *StorageMinio) GetBucket(name string) (Bucket, error) {
	bucket := Bucket{
		Name: name,
	}

	exists, err := s.Client.BucketExists(context.TODO(), bucket.Name)
	if err != nil || !exists {
		if err == nil {
			err = errors.New("bucket does not exist")
		}
		return bucket, err
	}

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

func (s *StorageMinio) ListFiles(bucket Bucket, dir string) ([]os.FileInfo, error) {
	var fileList []os.FileInfo
	objs := s.Client.ListObjects(context.TODO(), bucket.Name, minio.ListObjectsOptions{Prefix: dir})
	for obj := range objs {
		info := &utils.VirtualFile{
			FName:    path.Join(dir, obj.Key),
			FIsDir:   false,
			FSize:    obj.Size,
			FModTime: obj.LastModified,
		}
		fileList = append(fileList, info)
	}

	return fileList, nil
}

func (s *StorageMinio) DeleteBucket(bucket Bucket) error {
	return s.Client.RemoveBucket(context.TODO(), bucket.Name)
}

func (s *StorageMinio) GetFile(bucket Bucket, fpath string) (ReaderAtCloser, error) {
	obj, err := s.Client.GetObject(context.TODO(), bucket.Name, fpath, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}

	return obj, nil
}

func (s *StorageMinio) PutFile(bucket Bucket, fpath string, contents ReaderAtCloser) (string, error) {
	info, err := s.Client.PutObject(context.TODO(), bucket.Name, fpath, contents, -1, minio.PutObjectOptions{})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/%s", info.Bucket, info.Key), nil
}

func (s *StorageMinio) DeleteFile(bucket Bucket, fpath string) error {
	err := s.Client.RemoveObject(context.TODO(), bucket.Name, fpath, minio.RemoveObjectOptions{})
	if err != nil {
		return err
	}

	return nil
}
