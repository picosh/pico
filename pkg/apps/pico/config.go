package pico

import (
	"strings"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils"
)

func NewConfigSite(service string) *shared.ConfigSite {
	dbURL := utils.GetEnv("DATABASE_URL", "")
	tuns := utils.GetEnv("TUNS_CONSOLE_SECRET", "")
	withPipe := strings.ToLower(utils.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &shared.ConfigSite{
		DbURL:      dbURL,
		Space:      "pico",
		Logger:     shared.CreateLogger(service, withPipe),
		TunsSecret: tuns,
	}
}
