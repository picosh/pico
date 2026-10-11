package feeds

import (
	"html/template"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
)

// staticDir holds feeds' templates and public files.
const staticDir = "pkg/apps/feeds"

type Config struct {
	Debug       bool
	SendgridKey string
	Domain      string
	Port        string
	Protocol    string
	DbURL       string
	Space       string
	AllowedExt  []string
	Logger      *slog.Logger
}

func NewConfig(service string) *Config {
	debug := shared.GetEnv("FEEDS_DEBUG", "0")
	domain := shared.GetEnv("FEEDS_DOMAIN", "feeds.pico.sh")
	port := shared.GetEnv("FEEDS_WEB_PORT", "3000")
	protocol := shared.GetEnv("FEEDS_PROTOCOL", "https")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	sendgridKey := shared.GetEnv("SENDGRID_API_KEY", "")
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &Config{
		Debug:       debug == "1",
		SendgridKey: sendgridKey,
		Domain:      domain,
		Port:        port,
		Protocol:    protocol,
		DbURL:       dbURL,
		Space:       "feeds",
		AllowedExt:  []string{".txt", ".lxt"},
		Logger:      shared.CreateLogger(service, withPipe),
	}
}

func (c *Config) StaticPath(fname string) string {
	return filepath.Join(staticDir, fname)
}

func (c *Config) Static() router.Static {
	return router.Static{
		Dir:    staticDir,
		Logger: c.Logger,
		Site: shared.SitePageData{
			Domain:  template.URL(c.Domain),
			HomeURL: template.URL("//" + c.Domain),
		},
	}
}
