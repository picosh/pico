package main

import (
	"strings"

	"github.com/picosh/pico/pkg/apps/links"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils"
)

func main() {
	dbUrl := utils.GetEnv("DATABASE_URL", "")
	withPipe := strings.ToLower(utils.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"
	logger := shared.CreateLogger("links", withPipe)
	dbpool, err := links.NewDB(dbUrl, logger)
	if err != nil {
		panic(err)
	}
	cfg := links.NewLinksConfig(logger, dbpool)
	ch := make(chan error)
	links.StartSshServer(cfg, ch)
}
