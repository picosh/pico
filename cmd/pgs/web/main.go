package main

import (
	"context"
	"strings"

	"github.com/picosh/pico/pkg/apps/pgs"
	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/storage"
)

func main() {
	dbURL := shared.GetEnv("DATABASE_URL", "")
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"
	logger := shared.CreateLogger("pgs-web", withPipe)
	dbpool, err := pgsdb.NewDB(dbURL, logger)
	if err != nil {
		panic(err)
	}
	adapter := storage.GetStorageTypeFromEnv()
	st, err := storage.NewStorage(logger, adapter)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	drain := pgs.CreateSubCacheDrain(ctx, logger)
	pubsub := pgs.NewPubsubPipe(drain)
	defer func() {
		_ = pubsub.Close()
	}()
	cfg := pgs.NewPgsConfig(logger, dbpool, st, pubsub)
	pgs.StartApiServer(cfg)
}
