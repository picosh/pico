package feeds

import (
	"strings"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils"
)

func NewConfigSite(service string) *shared.ConfigSite {
	debug := utils.GetEnv("FEEDS_DEBUG", "0")
	domain := utils.GetEnv("FEEDS_DOMAIN", "feeds.pico.sh")
	port := utils.GetEnv("FEEDS_WEB_PORT", "3000")
	protocol := utils.GetEnv("FEEDS_PROTOCOL", "https")
	dbURL := utils.GetEnv("DATABASE_URL", "")
	sendgridKey := utils.GetEnv("SENDGRID_API_KEY", "")
	withPipe := strings.ToLower(utils.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &shared.ConfigSite{
		Debug:       debug == "1",
		SendgridKey: sendgridKey,
		Domain:      domain,
		Port:        port,
		Protocol:    protocol,
		DbURL:       dbURL,
		Space:       "feeds",
		AllowedExt:  []string{".txt"},
		HiddenPosts: []string{"_header.txt", "_readme.txt"},
		Logger:      shared.CreateLogger(service, withPipe),
	}
}
