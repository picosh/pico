package main

import (
	"bytes"
	"fmt"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/db/postgres"
	"git.sr.ht/~erock/pico/imgs"
	"git.sr.ht/~erock/pico/imgs/storage"
	"git.sr.ht/~erock/pico/shared"
)

func main() {
	cfg := imgs.NewConfigSite()
	dbp := postgres.NewDB(&cfg.ConfigCms)

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

		reader, err := st.GetFile(bucket, post.Filename)
		if err != nil {
			cfg.Logger.Infof("file not found %s/%s", post.UserID, post.Filename)
			continue
		}
		defer reader.Close()

		opt := shared.NewImgOptimizer(cfg.Logger, "")
		contents := &bytes.Buffer{}
		img, err := opt.GetImage(reader, post.MimeType)
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
			storage.NopReaderAtCloser(webpReader),
		)
		if err != nil {
			cfg.Logger.Error(err)
		}
	}
}
