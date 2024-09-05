package pubsub

import (
	"github.com/picosh/pico/shared"
)

func NewConfigSite() *shared.ConfigSite {
	domain := shared.GetEnv("PUBSUB_DOMAIN", "send.pico.sh")
	port := shared.GetEnv("PUBSUB_WEB_PORT", "3000")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	protocol := shared.GetEnv("PUBSUB_PROTOCOL", "https")

	return &shared.ConfigSite{
		Domain:   domain,
		Port:     port,
		Protocol: protocol,
		DbURL:    dbURL,
		Logger:   shared.CreateLogger(),
		Space:    "pubsub",
	}
}
