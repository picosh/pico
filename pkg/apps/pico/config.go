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
	// CustomDomainTargets are the hosts a custom domain can point at.
	CustomDomainTargets []string
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
		CustomDomainTargets: []string{
			hostOnly(shared.GetEnv("PGS_DOMAIN", "pgs.sh")),
			hostOnly(shared.GetEnv("PROSE_DOMAIN", "prose.sh")),
			"tuns.sh",
			"nue.tuns.sh",
		},
	}
}

func hostOnly(domain string) string {
	host, _, _ := strings.Cut(domain, ":")
	return host
}
