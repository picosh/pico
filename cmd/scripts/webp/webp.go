package main

import (
	"bytes"
	"fmt"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/imgs"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/send/utils"
)

func main() {
	cfg := imgs.NewConfigSite()
	dbp := postgres.NewDB(cfg.DbURL, cfg.Logger)

	cfg.Logger.Info("fetching all img posts")
	posts, err := dbp.FindAllPosts(&db.Pager{Num: 1000, Page: 0}, "imgs")
	if err != nil {
		panic(err)
	}

	var st storage.ObjectStorage
	if cfg.MinioURL == "" {
		st, err = storage.NewStorageFS(cfg.StorageDir)
	} else {
		st, err = storage.NewStorageMinio(cfg.MinioURL, cfg.MinioUser, cfg.MinioPass)
	}

	if err != nil {
		panic(err)
	}

	total := len(posts.Data)
	cfg.Logger.Infof("%d posts", total)
	for i, post := range posts.Data {
		cfg.Logger.Infof("%d%% %s %s", ((i+1)/total)*100, post.Filename, post.MimeType)
		bucket, err := st.GetBucket(post.UserID)
		if err != nil {
			cfg.Logger.Infof("bucket not found %s", post.UserID)
			continue
		}

		reader, _, _, err := st.GetFile(bucket, post.Filename)
		if err != nil {
			cfg.Logger.Infof("file not found %s/%s", post.UserID, post.Filename)
			continue
		}
		defer reader.Close()

		opt := imgs.NewImgOptimizer(cfg.Logger, "")
		contents := &bytes.Buffer{}
		img, err := imgs.GetImageForOptimization(reader, post.MimeType)
		if err != nil {
			cfg.Logger.Error(err)
			continue
		}

		err = opt.EncodeWebp(contents, img)
		if err != nil {
			cfg.Logger.Error(err)
			continue
		}

		webpReader := bytes.NewReader(contents.Bytes())
		_, err = st.PutFile(
			bucket,
			fmt.Sprintf("%s.webp", shared.SanitizeFileExt(post.Filename)),
			utils.NopReaderAtCloser(webpReader),
			&utils.FileEntry{},
		)
		if err != nil {
			cfg.Logger.Error(err)
		}
	}
}
