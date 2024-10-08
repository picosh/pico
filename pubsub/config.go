package pubsub

import (
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

func NewConfigSite() *shared.ConfigSite {
	domain := utils.GetEnv("PUBSUB_DOMAIN", "pipe.pico.sh")
	port := utils.GetEnv("PUBSUB_WEB_PORT", "3000")
	dbURL := utils.GetEnv("DATABASE_URL", "")
	protocol := utils.GetEnv("PUBSUB_PROTOCOL", "https")

	return &shared.ConfigSite{
		Domain:   domain,
		Port:     port,
		Protocol: protocol,
		DbURL:    dbURL,
		Logger:   shared.CreateLogger("pubsub"),
		Space:    "pubsub",
	}
}
