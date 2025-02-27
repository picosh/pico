package main

import (
	"github.com/picosh/pico/pgs"
	pgsdb "github.com/picosh/pico/pgs/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/utils"
)

func main() {
	minioURL := utils.GetEnv("MINIO_URL", "")
	minioUser := utils.GetEnv("MINIO_ROOT_USER", "")
	minioPass := utils.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := utils.GetEnv("DATABASE_URL", "")
	logger := shared.CreateLogger("pgs-ssh")
	dbpool, err := pgsdb.NewDB(dbURL, logger)
	if err != nil {
		panic(err)
	}
	st, err := storage.NewStorageMinio(logger, minioURL, minioUser, minioPass)
	if err != nil {
		panic(err)
	}
	cfg := pgs.NewPgsConfig(logger, dbpool, st)
	killCh := make(chan error)
	pgs.StartSshServer(cfg, killCh)
}
