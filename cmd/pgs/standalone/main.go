package main

import (
	"github.com/picosh/pico/pkg/apps/pgs"
	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/storage"
	"github.com/picosh/utils"
)

func main() {
	dbURL := utils.GetEnv("DATABASE_URL", "./data/pgs.sqlite3")
	logger := shared.CreateLogger("pgs-standalone", false)
	dbpool, err := pgsdb.NewSqliteDB(dbURL, logger)
	if err != nil {
		panic(err)
	}
	adapter := storage.GetStorageTypeFromEnv()
	st, err := storage.NewStorage(logger, adapter)
	if err != nil {
		panic(err)
	}
	pubsub := pgs.NewPubsubChan()
	defer func() {
		_ = pubsub.Close()
	}()
	cfg := pgs.NewPgsConfig(logger, dbpool, st, pubsub)
	killCh := make(chan error)

	go pgs.StartApiServer(cfg)
	pgs.StartSshServer(cfg, killCh)
}
