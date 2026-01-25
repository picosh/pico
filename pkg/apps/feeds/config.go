package feeds

import (
	"strings"

	"github.com/picosh/pico/pkg/shared"
)

func NewConfigSite(service string) *shared.ConfigSite {
	debug := shared.GetEnv("FEEDS_DEBUG", "0")
	domain := shared.GetEnv("FEEDS_DOMAIN", "feeds.pico.sh")
	port := shared.GetEnv("FEEDS_WEB_PORT", "3000")
	protocol := shared.GetEnv("FEEDS_PROTOCOL", "https")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	sendgridKey := shared.GetEnv("SENDGRID_API_KEY", "")
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

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
