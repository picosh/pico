package pipe

import (
	"strings"

	"github.com/picosh/pico/pkg/shared"
)

func NewConfigSite(service string) *shared.ConfigSite {
	domain := shared.GetEnv("PIPE_DOMAIN", "pipe.pico.sh")
	port := shared.GetEnv("PIPE_WEB_PORT", "3000")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	protocol := shared.GetEnv("PIPE_PROTOCOL", "https")
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &shared.ConfigSite{
		Domain:   domain,
		Port:     port,
		Protocol: protocol,
		DbURL:    dbURL,
		Logger:   shared.CreateLogger(service, withPipe),
		Space:    "pipe",
	}
}
