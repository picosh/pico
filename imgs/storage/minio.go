package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type StorageMinio struct {
	Client *minio.Client
}

func NewStorageMinio(address, user, pass string) (*StorageMinio, error) {
	endpoint, err := url.Parse(address)
	if err != nil {
		return nil, err
	}

	mClient, err := minio.New(endpoint.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(user, pass, ""),
		Secure: endpoint.Scheme == "https",
	})

	if err != nil {
		return nil, err
	}

	return &StorageMinio{
		Client: mClient,
	}, err
}

// GetBucket - A bucket for the filesystem is just a directory.
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

func (s *StorageMinio) GetFile(bucket Bucket, fname string) ([]byte, error) {
	obj, err := s.Client.GetObject(context.TODO(), bucket.Name, fname, minio.GetObjectOptions{})
	if err != nil {
		return []byte{}, err
	}

	dat, err := io.ReadAll(obj)
	if err != nil {
		return []byte{}, err
	}

	return dat, nil
}

func (s *StorageMinio) PutFile(bucket Bucket, fname string, contents []byte) (string, error) {
	info, err := s.Client.PutObject(context.TODO(), bucket.Name, fname, bytes.NewReader(contents), int64(len(contents)), minio.PutObjectOptions{})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/%s", info.Bucket, info.Key), nil
}

func (s *StorageMinio) DeleteFile(bucket Bucket, fname string) error {
	err := s.Client.RemoveObject(context.TODO(), bucket.Name, fname, minio.RemoveObjectOptions{})
	if err != nil {
		return err
	}

	return nil
}
