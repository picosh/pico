package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/picosh/pico/pkg/send/utils"
)

type StorageS3 struct {
	Client *s3.Client
	Region string
}

var _ ObjectStorage = &StorageS3{}
var _ ObjectStorage = (*StorageS3)(nil)

func NewStorageS3(region, key, secret string) (*StorageS3, error) {
	creds := credentials.NewStaticCredentialsProvider(key, secret, "")
	cfg, err := config.LoadDefaultConfig(
		context.TODO(),
		config.WithRegion(region),
		config.WithCredentialsProvider(creds),
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(cfg)
	return &StorageS3{Client: client}, nil
}

func (s *StorageS3) GetBucket(name string) (Bucket, error) {
	bucket := Bucket{
		Name: name,
	}

	_, err := s.Client.HeadBucket(context.TODO(), &s3.HeadBucketInput{
		Bucket: aws.String(name),
	})
	if err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) {
			switch apiError.(type) {
			case *types.NotFound:
				return bucket, fmt.Errorf("bucket not found")
			default:
				return bucket, err
			}
		}
	}
	return bucket, nil
}

func (s *StorageS3) UpsertBucket(name string) (Bucket, error) {
	bucket, err := s.GetBucket(name)
	if err == nil {
		return bucket, nil
	}

	_, err = s.Client.CreateBucket(context.TODO(), &s3.CreateBucketInput{
		Bucket: aws.String(name),
		CreateBucketConfiguration: &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(s.Region),
		},
	})

	return bucket, err
}

func (s *StorageS3) GetBucketQuota(bucket Bucket) (uint64, error) {
	var totalSize uint64
	paginator := s3.NewListObjectsV2Paginator(s.Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket.Name),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			return 0, err
		}

		for _, object := range page.Contents {
			totalSize += uint64(*object.Size)
		}
	}

	return totalSize, nil
}

func (s *StorageS3) ListBuckets() ([]string, error) {
	bcks := []string{}
	maxBuckets := int32(1000)
	result, err := s.Client.ListBuckets(context.TODO(), &s3.ListBucketsInput{MaxBuckets: &maxBuckets})
	if err != nil {
		return bcks, err
	}

	for _, bucket := range result.Buckets {
		bcks = append(bcks, *bucket.Name)
	}

	return bcks, nil
}

func (s *StorageS3) ListObjects(bucket Bucket, dir string, recursive bool) ([]os.FileInfo, error) {
	var fileList []os.FileInfo

	prefix := strings.TrimPrefix(dir, "/")
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket.Name),
		Prefix: aws.String(prefix),
	}
	if !recursive {
		input.Delimiter = aws.String("/")
	}

	paginator := s3.NewListObjectsV2Paginator(s.Client, input)

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			return fileList, err
		}

		for _, pref := range page.CommonPrefixes {
			modTime := time.Time{}
			fname := strings.TrimSuffix(strings.TrimPrefix(*pref.Prefix, prefix), "/")
			info := &utils.VirtualFile{
				FName:    fname,
				FIsDir:   true,
				FSize:    0,
				FModTime: modTime,
			}
			fileList = append(fileList, info)
		}

		for _, obj := range page.Contents {
			modTime := obj.LastModified
			fname := strings.TrimSuffix(strings.TrimPrefix(*obj.Key, prefix), "/")
			info := &utils.VirtualFile{
				FName:    fname,
				FIsDir:   false,
				FSize:    *obj.Size,
				FModTime: *modTime,
			}
			fileList = append(fileList, info)
		}
	}

	return fileList, nil
}

func (s *StorageS3) deleteAllObjects(bucket Bucket) error {
	paginator := s3.NewListObjectsV2Paginator(s.Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket.Name),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			return err
		}

		var objectIdentifiers []types.ObjectIdentifier
		for _, object := range page.Contents {
			objectIdentifiers = append(objectIdentifiers, types.ObjectIdentifier{Key: object.Key})
		}

		_, err = s.Client.DeleteObjects(context.TODO(), &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket.Name),
			Delete: &types.Delete{
				Objects: objectIdentifiers,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *StorageS3) DeleteBucket(bucket Bucket) error {
	err := s.deleteAllObjects(bucket)
	if err != nil {
		return err
	}

	_, err = s.Client.DeleteBucket(context.TODO(), &s3.DeleteBucketInput{
		Bucket: aws.String(bucket.Name),
	})
	return err
}

func (s *StorageS3) GetObject(bucket Bucket, fpath string) (utils.ReadAndReaderAtCloser, *ObjectInfo, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(bucket.Name),
		Key:    aws.String(fpath),
	}

	objInfo := &ObjectInfo{
		LastModified: time.Time{},
		Metadata:     nil,
		UserMetadata: map[string]string{},
	}

	result, err := s.Client.GetObject(context.TODO(), input)
	if err != nil {
		return nil, objInfo, err
	}

	objInfo.UserMetadata = result.Metadata
	objInfo.ETag = *result.ETag
	objInfo.Size = *result.ContentLength
	objInfo.LastModified = *result.LastModified

	// unfortunately we have to read the object into memory because we
	// require io.ReadAt
	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, objInfo, err
	}
	defer result.Body.Close()

	// Create a bytes.Reader which implements io.ReaderAt
	body := bytes.NewReader(data)
	content := utils.NopReadAndReaderAtCloser(body)

	return content, objInfo, nil
}

func (s *StorageS3) PutObject(bucket Bucket, fpath string, contents io.Reader, entry *utils.FileEntry) (string, int64, error) {
	key := strings.TrimPrefix(fpath, "/")
	uploader := manager.NewUploader(s.Client)
	info, err := uploader.Upload(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(bucket.Name),
		Key:    aws.String(key),
		Body:   contents,
	})
	if err != nil {
		return "", 0, err
	}

	return fmt.Sprintf("%s/%s", bucket.Name, *info.Key), entry.Size, nil
}

func (s *StorageS3) DeleteObject(bucket Bucket, fpath string) error {
	key := strings.TrimPrefix(fpath, "/")
	_, err := s.Client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket.Name),
		Key:    aws.String(key),
	})
	return err
}
