package pico

import (
	"github.com/picosh/pico/shared"
)

func NewConfigSite() *shared.ConfigSite {
	dbURL := shared.GetEnv("DATABASE_URL", "")

	return &shared.ConfigSite{
		DbURL:  dbURL,
		Space:  "pico",
		Logger: shared.CreateLogger(),
	}
}
