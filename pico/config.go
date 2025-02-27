package pico

import (
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

func NewConfigSite(service string) *shared.ConfigSite {
	dbURL := utils.GetEnv("DATABASE_URL", "")

	return &shared.ConfigSite{
		DbURL:  dbURL,
		Space:  "pico",
		Logger: shared.CreateLogger(service),
	}
}
