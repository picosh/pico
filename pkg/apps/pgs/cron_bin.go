package pgs

import (
	"log/slog"
	"time"

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
)

const binRetentionDays = 14

func BinCron(cfg *PgsConfig) {
	logger := cfg.Logger
	storage := cfg.Storage
	db := cfg.DB

	// Loop every 10 minutes
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	deleteOldBinObjects(logger, db, storage)

	for range ticker.C {
		deleteOldBinObjects(logger, db, storage)
	}
}

func deleteOldBinObjects(logger *slog.Logger, db pgsdb.PgsDB, storage storage.StorageServe) {
	logger.Info("running bin cron")
	users, err := db.FindUsers()
	if err != nil {
		logger.Error("failed to find users", "error", err)
		return
	}

	for _, user := range users {
		log := shared.LoggerWithUser(logger, user)
		bucketName := shared.GetAssetBucketName(user.ID)
		bucket, err := storage.GetBucket(bucketName)
		if err != nil {
			log.Error("failed to get bucket", "bucket", bucketName, "error", err)
			continue
		}

		project, err := db.FindProjectByName(user.ID, "bin")
		if err != nil {
			continue
		}

		log = log.With("project", project.Name)

		objs, err := storage.ListObjects(bucket, project.ProjectDir+"/", true)
		if err != nil {
			log.Error("failed to list objects", "error", err)
			continue
		}

		cutoff := time.Now().Add(-binRetentionDays * 24 * time.Hour)
		for _, obj := range objs {
			if obj.IsDir() {
				continue
			}

			if obj.ModTime().Before(cutoff) {
				objPath := project.ProjectDir + "/" + obj.Name()
				if err := storage.DeleteObject(bucket, objPath); err != nil {
					logger.Error("failed to delete old object", "file", obj.Name(), "error", err)
				} else {
					logger.Info("deleted old object", "file", obj.Name(), "age", time.Since(obj.ModTime()))
				}
			}
		}
	}
}
