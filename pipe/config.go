package pipe

import (
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

func NewConfigSite() *shared.ConfigSite {
	domain := utils.GetEnv("PIPE_DOMAIN", "pipe.pico.sh")
	port := utils.GetEnv("PIPE_WEB_PORT", "3000")
	dbURL := utils.GetEnv("DATABASE_URL", "")
	protocol := utils.GetEnv("PIPE_PROTOCOL", "https")

	return &shared.ConfigSite{
		Domain:   domain,
		Port:     port,
		Protocol: protocol,
		DbURL:    dbURL,
		Logger:   shared.CreateLogger("pipe"),
		Space:    "pipe",
	}
}
