package pico

import (
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils"
)

func NewConfigSite(service string) *shared.ConfigSite {
	dbURL := utils.GetEnv("DATABASE_URL", "")
	tuns := utils.GetEnv("TUNS_CONSOLE_SECRET", "")

	return &shared.ConfigSite{
		DbURL:      dbURL,
		Space:      "pico",
		Logger:     shared.CreateLogger(service),
		TunsSecret: tuns,
	}
}
