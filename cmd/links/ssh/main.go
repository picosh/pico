package main

import (
	"github.com/picosh/pico/links"
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

func main() {
	dbUrl := utils.GetEnv("DATABASE_URL", "")
	logger := shared.CreateLogger("links")
	dbpool, err := links.NewDB(dbUrl, logger)
	if err != nil {
		panic(err)
	}
	cfg := links.NewLinksConfig(logger, dbpool)
	ch := make(chan error)
	links.StartSshServer(cfg, ch)
}
