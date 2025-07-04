package pastes

import (
	"strings"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils"
)

func NewConfigSite(service string) *shared.ConfigSite {
	debug := utils.GetEnv("PASTES_DEBUG", "0")
	domain := utils.GetEnv("PASTES_DOMAIN", "pastes.sh")
	port := utils.GetEnv("PASTES_WEB_PORT", "3000")
	dbURL := utils.GetEnv("DATABASE_URL", "")
	protocol := utils.GetEnv("PASTES_PROTOCOL", "https")
	withPipe := strings.ToLower(utils.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &shared.ConfigSite{
		Debug:        debug == "1",
		Domain:       domain,
		Port:         port,
		Protocol:     protocol,
		DbURL:        dbURL,
		Space:        "pastes",
		Logger:       shared.CreateLogger(service, withPipe),
		MaxAssetSize: int64(3 * utils.MB),
	}
}
