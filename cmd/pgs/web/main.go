package main

import (
	"github.com/picosh/pico/pkg/apps/pgs"
	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/storage"
	"github.com/picosh/utils"
)

func main() {
	minioURL := utils.GetEnv("MINIO_URL", "")
	minioUser := utils.GetEnv("MINIO_ROOT_USER", "")
	minioPass := utils.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := utils.GetEnv("DATABASE_URL", "")
	logger := shared.CreateLogger("pgs-web")
	dbpool, err := pgsdb.NewDB(dbURL, logger)
	if err != nil {
		panic(err)
	}
	st, err := storage.NewStorageMinio(logger, minioURL, minioUser, minioPass)
	if err != nil {
		panic(err)
	}
	cfg := pgs.NewPgsConfig(logger, dbpool, st)
	pgs.StartApiServer(cfg)
}
