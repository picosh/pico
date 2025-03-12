package main

import (
	"context"
	"net/url"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/picosh/pico/pkg/apps/prose"
	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/shared"
)

func bail(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	cfg := prose.NewConfigSite("prose-rm-old-buckets")
	logger := cfg.Logger
	picoDb := postgres.NewDB(cfg.DbURL, logger)
	endpoint, err := url.Parse(cfg.MinioURL)
	bail(err)
	ssl := endpoint.Scheme == "https"
	mClient, err := minio.New(endpoint.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioUser, cfg.MinioPass, ""),
		Secure: ssl,
	})
	bail(err)

	users, err := picoDb.FindUsers()
	bail(err)
	ctx := context.TODO()

	for _, user := range users {
		logger.Info("deleting old buckets", "user", user.Name)
		bucketName := shared.GetImgsBucketName(user.ID)

		exists, err := mClient.BucketExists(ctx, bucketName)
		if err != nil {
			logger.Error("bucket exists", "err", err)
		}

		if !exists {
			continue
		}

		objectsCh := make(chan minio.ObjectInfo)
		go func() {
			defer close(objectsCh)
			opts := minio.ListObjectsOptions{Prefix: "", Recursive: true}
			for object := range mClient.ListObjects(ctx, bucketName, opts) {
				logger.Info("object", "name", object.Key)
				if object.Err != nil {
					logger.Error("list objects", "err", err)
				}
				objectsCh <- object
			}
		}()

		errorCh := mClient.RemoveObjects(ctx, bucketName, objectsCh, minio.RemoveObjectsOptions{})

		for e := range errorCh {
			logger.Error("remove obj", "err", e)
		}

		logger.Info("removing bucket", "user", user.Name, "bucket", bucketName)
		err = mClient.RemoveBucket(ctx, bucketName)
		if err != nil {
			logger.Error("remove bucket", "err", err)
		}

		logger.Info("Success!", "user", user.Name)
	}
}
