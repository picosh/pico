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

func images(logger *slog.Logger, dbh db.DB, st storage.StorageServe, bucket sst.Bucket, user *db.User) error {
	posts, err := dbh.FindPostsForUser(&db.Pager{Num: 2000, Page: 0}, user.ID, "imgs")
	if err != nil {
		return err
	}

	if len(posts.Data) == 0 {
		logger.Info("user does not have any images, skipping")
		return nil
	}

	imgBucket, err := st.GetBucket(shared.GetImgsBucketName(user.ID))
	if err != nil {
		logger.Info("user does not have an images dir, skipping")
		return nil
	}

	/* imgs, err := st.ListObjects(imgBucket, "/", false)
	if err != nil {
		return err
	} */

	for _, posts := range posts.Data {
		rdr, _, err := st.GetObject(imgBucket, posts.Filename)
		if err != nil {
			logger.Error("get object", "err", err)
			continue
		}
		err = upload(logger, st, bucket, posts.Filename, rdr)
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
		if user.Name != "erock" {
			continue
		}
		logger.Info("migrating user images", "user", user.Name)

		bucket, err := st.UpsertBucket(shared.GetAssetBucketName(user.ID))
		bail(err)
		_, _ = picoDb.InsertProject(user.ID, "prose", "prose")
		err = images(logger, picoDb, st, bucket, user)
		if err != nil {
			logger.Error("image uploader", "err", err)
		}
	}
}
