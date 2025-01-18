package main

import (
	"bufio"
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/picosh/pico/db/postgres"
	fileshared "github.com/picosh/pico/filehandlers/shared"
	"github.com/picosh/pico/prose"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
)

func bail(err error) {
	if err != nil {
		panic(err)
	}
}

type RenderEvent struct {
	UserID  string
	Service string
}

// run queue on an interval to merge file uploads from same user.
func render(ssg *prose.SSG, ch chan RenderEvent) {
	var pendingFlushes sync.Map
	tick := time.Tick(10 * time.Second)
	for {
		select {
		case event := <-ch:
			ssg.Logger.Info("received request to generate blog", "userId", event.UserID)
			pendingFlushes.Store(event.UserID, event.Service)
		case <-tick:
			ssg.Logger.Info("flushing ssg requests")
			go func() {
				pendingFlushes.Range(func(key, value any) bool {
					pendingFlushes.Delete(key)
					event := value.(RenderEvent)
					user, err := ssg.DB.FindUser(event.UserID)
					if err != nil {
						ssg.Logger.Error("cannot find user", "err", err)
						return true
					}

					bucket, err := ssg.Storage.GetBucket(shared.GetAssetBucketName(user.ID))
					if err != nil {
						ssg.Logger.Error("cannot find bucket", "err", err)
						return true
					}

					err = ssg.ProseBlog(user, bucket, event.Service)
					if err != nil {
						ssg.Logger.Error("cannot generate blog", "err", err)
					}
					return true
				})
			}()
		}
	}
}

func main() {
	cfg := prose.NewConfigSite()
	logger := cfg.Logger
	picoDb := postgres.NewDB(cfg.DbURL, logger)
	st, err := storage.NewStorageMinio(logger, cfg.MinioURL, cfg.MinioUser, cfg.MinioPass)
	bail(err)

	ssg := &prose.SSG{
		Cfg:       cfg,
		DB:        picoDb,
		Storage:   st,
		Logger:    cfg.Logger,
		TmplDir:   "./prose/html",
		StaticDir: "./prose/public",
	}

	ctx := context.Background()
	drain := fileshared.CreateSubUploadDrain(ctx, cfg.Logger)

	ch := make(chan RenderEvent)
	go render(ssg, ch)

	for {
		scanner := bufio.NewScanner(drain)
		for scanner.Scan() {
			var data fileshared.FileUploaded

			err := json.Unmarshal(scanner.Bytes(), &data)
			if err != nil {
				logger.Error("json unmarshal", "err", err)
				continue
			}

			// we don't care about any other pgs sites so ignore them
			if data.Service == "pgs" && data.ProjectName != "prose" {
				continue
			}

			logger = logger.With(
				"userId", data.UserID,
				"filename", data.Filename,
				"action", data.Action,
				"project", data.ProjectName,
				"service", data.Service,
			)

			bucket, err := ssg.Storage.GetBucket(shared.GetAssetBucketName(data.UserID))
			if err != nil {
				ssg.Logger.Error("cannot find bucket", "err", err)
				continue
			}
			user, err := ssg.DB.FindUser(data.UserID)
			if err != nil {
				logger.Error("cannot find user", "err", err)
				continue
			}

			if data.Action == "delete" {
				err = st.DeleteObject(bucket, data.Filename)
				if err != nil {
					logger.Error("cannot delete object", "err", err)
				}
				post, err := ssg.DB.FindPostWithFilename(data.Filename, data.UserID, "prose")
				if err != nil {
					logger.Error("cannot find post", "err", err)
				} else {
					err = ssg.DB.RemovePosts([]string{post.ID})
					if err != nil {
						logger.Error("cannot delete post", "err", err)
					}
				}
				ch <- RenderEvent{data.UserID, data.Service}
			} else if data.Action == "create" || data.Action == "update" {
				_, err := ssg.UpsertPost(user.ID, user.Name, bucket, data.Filename)
				if err != nil {
					logger.Error("cannot upsert post", "err", err)
					continue
				}
				ch <- RenderEvent{data.UserID, data.Service}
			}
		}
	}
}
