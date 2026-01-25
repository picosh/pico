package pico

import (
	"strings"

	"github.com/picosh/pico/pkg/shared"
)

func NewConfigSite(service string) *shared.ConfigSite {
	dbURL := shared.GetEnv("DATABASE_URL", "")
	tuns := shared.GetEnv("TUNS_CONSOLE_SECRET", "")
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &shared.ConfigSite{
		DbURL:      dbURL,
		Space:      "pico",
		Logger:     shared.CreateLogger(service, withPipe),
		TunsSecret: tuns,
	}
}
