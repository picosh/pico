package pipe

import (
	"strings"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils"
)

func NewConfigSite(service string) *shared.ConfigSite {
	domain := utils.GetEnv("PIPE_DOMAIN", "pipe.pico.sh")
	port := utils.GetEnv("PIPE_WEB_PORT", "3000")
	dbURL := utils.GetEnv("DATABASE_URL", "")
	protocol := utils.GetEnv("PIPE_PROTOCOL", "https")
	withPipe := strings.ToLower(utils.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &shared.ConfigSite{
		Domain:   domain,
		Port:     port,
		Protocol: protocol,
		DbURL:    dbURL,
		Logger:   shared.CreateLogger(service, withPipe),
		Space:    "pipe",
	}
}
