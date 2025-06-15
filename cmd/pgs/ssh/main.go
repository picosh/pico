package main

import (
	"github.com/picosh/pico/pkg/apps/pgs"
	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/storage"
	"github.com/picosh/utils"
)

func main() {
	dbURL := utils.GetEnv("DATABASE_URL", "")
	logger := shared.CreateLogger("pgs-ssh")
	dbpool, err := pgsdb.NewDB(dbURL, logger)
	if err != nil {
		panic(err)
	}
	adapter := storage.GetStorageTypeFromEnv()
	st, err := storage.NewStorage(logger, adapter)
	if err != nil {
		panic(err)
	}
	cfg := pgs.NewPgsConfig(logger, dbpool, st)
	killCh := make(chan error)
	pgs.StartSshServer(cfg, killCh)
}
