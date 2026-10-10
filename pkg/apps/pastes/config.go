package pastes

import (
	"strings"

	"github.com/picosh/pico/pkg/shared"
)

func NewConfigSite(service string) *shared.ConfigSite {
	debug := shared.GetEnv("PASTES_DEBUG", "0")
	domain := shared.GetEnv("PASTES_DOMAIN", "pastes.sh")
	port := shared.GetEnv("PASTES_WEB_PORT", "3000")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	protocol := shared.GetEnv("PASTES_PROTOCOL", "https")
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &shared.ConfigSite{
		Debug:        debug == "1",
		Domain:       domain,
		Port:         port,
		Protocol:     protocol,
		DbURL:        dbURL,
		Space:        "pastes",
		Logger:       shared.CreateLogger(service, withPipe),
		MaxAssetSize: int64(3 * shared.MB),
	}
}
