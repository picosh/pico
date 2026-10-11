package pico

import (
	"log/slog"
	"strings"

	"github.com/picosh/pico/pkg/shared"
)

type Config struct {
	DbURL      string
	Space      string
	TunsSecret string
	Logger     *slog.Logger
}

func NewConfig(service string) *Config {
	dbURL := shared.GetEnv("DATABASE_URL", "")
	tuns := shared.GetEnv("TUNS_CONSOLE_SECRET", "")
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &Config{
		DbURL:      dbURL,
		Space:      "pico",
		Logger:     shared.CreateLogger(service, withPipe),
		TunsSecret: tuns,
	}
}
