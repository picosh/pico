package main

import (
	"bytes"
	"io"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/prose"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	sst "github.com/picosh/pobj/storage"
	sendUtils "github.com/picosh/send/utils"
)

func bail(err error) {
	if err != nil {
		panic(err)
	}
}

func upload(logger *slog.Logger, st storage.StorageServe, bucket sst.Bucket, fpath string, rdr io.Reader) error {
	toSite := filepath.Join("prose", fpath)
	logger.Info("uploading object", "bucket", bucket.Name, "object", toSite)
	buf := &bytes.Buffer{}
	size, err := io.Copy(buf, rdr)
	if err != nil {
		return err
	}

	_, _, err = st.PutObject(bucket, toSite, buf, &sendUtils.FileEntry{
		Mtime: time.Now().Unix(),
		Size:  size,
	})
	return err
}

func images(logger *slog.Logger, st storage.StorageServe, bucket sst.Bucket, user *db.User) error {
	imgBucket, err := st.GetBucket(shared.GetImgsBucketName(user.ID))
	if err != nil {
		logger.Info("user does not have an images dir, skipping")
		return nil
	}
	imgs, err := st.ListObjects(imgBucket, "/", false)
	if err != nil {
		return err
	}

	for _, inf := range imgs {
		rdr, _, err := st.GetObject(imgBucket, inf.Name())
		if err != nil {
			return err
		}
		err = upload(logger, st, bucket, inf.Name(), rdr)
		if err != nil {
			return err
		}
	}

	return nil
}

func main() {
	cfg := prose.NewConfigSite()
	logger := cfg.Logger
	picoDb := postgres.NewDB(cfg.DbURL, logger)
	st, err := storage.NewStorageMinio(logger, cfg.MinioURL, cfg.MinioUser, cfg.MinioPass)
	bail(err)

	users, err := picoDb.FindUsers()
	bail(err)

	for _, user := range users {
		bucket, err := st.UpsertBucket(shared.GetAssetBucketName(user.ID))
		bail(err)
		_, _ = picoDb.InsertProject(user.ID, "prose", "prose")
		bail(images(logger, st, bucket, user))
	}
}
