package main

import (
	"os"
	"strings"

	"github.com/picosh/pico/pkg/apps/pgs"
	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/storage"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
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

	// first time user experience flow
	args := os.Args
	if len(args) > 0 {
		if args[1] == "init" {
			if len(args) < 4 {
				panic("must provide username and pubkey")
			}

			userName := args[2]
			pubkeyRaw := strings.Join(args[3:], " ")
			key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubkeyRaw))
			if err != nil {
				logger.Error("parse pubkey", "err", err)
				return
			}
			pubkey := utils.KeyForKeyText(key)
			logger.Info("init cli", "userName", userName, "pubkey", pubkey)

			err = dbpool.RegisterAdmin(userName, pubkey)
			if err != nil {
				panic(err)
			}
			logger.Info("Admin user created. You can now start using pgs!")
			return
		}
	}

	go pgs.StartApiServer(cfg)
	pgs.StartSshServer(cfg, killCh)
}
