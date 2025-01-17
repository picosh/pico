package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/filehandlers"
	"github.com/picosh/pico/prose"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/utils/pipe"
)

func bail(err error) {
	if err != nil {
		panic(err)
	}
}

func render(ssg *prose.SSG, ch chan string) {
	var pendingFlushes sync.Map
	tick := time.Tick(10 * time.Second)
	for {
		select {
		case userID := <-ch:
			ssg.Logger.Info("received request to generate blog", "userId", userID)
			pendingFlushes.Store(userID, userID)
		case <-tick:
			ssg.Logger.Info("flushing ssg requests")
			go func() {
				pendingFlushes.Range(func(key, value any) bool {
					pendingFlushes.Delete(key)
					user, err := ssg.DB.FindUser(value.(string))
					if err != nil {
						ssg.Logger.Error("cannot find user", "err", err)
						return true
					}

					bucket, err := ssg.Storage.GetBucket(shared.GetAssetBucketName(user.ID))
					if err != nil {
						ssg.Logger.Error("cannot find bucket", "err", err)
						return true
					}

					err = ssg.ProseBlog(user, bucket)
					if err != nil {
						ssg.Logger.Error("cannot generate blog", "err", err)
					}
					return true
				})
			}()
		}
	}
}

func createSubProseDrain(ctx context.Context, logger *slog.Logger) *pipe.ReconnectReadWriteCloser {
	info := shared.NewPicoPipeClient()
	send := pipe.NewReconnectReadWriteCloser(
		ctx,
		logger,
		info,
		"sub to prose-drain",
		"sub prose-drain -k",
		100,
		-1,
	)
	return send
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
	drain := createSubProseDrain(ctx, cfg.Logger)

	ch := make(chan string)
	go render(ssg, ch)

	for {
		scanner := bufio.NewScanner(drain)
		for scanner.Scan() {
			var data filehandlers.SuccesHook

			err := json.Unmarshal(scanner.Bytes(), &data)
			if err != nil {
				logger.Error("json unmarshal", "err", err)
				continue
			}

			logger = logger.With(
				"userId", data.UserID,
				"filename", data.Filename,
				"action", data.Action,
			)

			if data.Action == "delete" {
				bucket, err := ssg.Storage.GetBucket(shared.GetAssetBucketName(data.UserID))
				if err != nil {
					ssg.Logger.Error("cannot find bucket", "err", err)
					continue
				}
				err = st.DeleteObject(bucket, data.Filename)
				if err != nil {
					logger.Error("cannot delete object", "err", err)
					continue
				}
				ch <- data.UserID
			} else if data.Action == "create" || data.Action == "update" {
				ch <- data.UserID
			}
		}
	}
}
