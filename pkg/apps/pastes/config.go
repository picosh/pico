package pastes

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
)

// staticDir holds pastes' templates and public files.
const staticDir = "pkg/apps/pastes"

type Config struct {
	Debug        bool
	Domain       string
	Port         string
	Protocol     string
	DbURL        string
	Space        string
	MaxAssetSize int64
	Logger       *slog.Logger
}

func NewConfig(service string) *Config {
	debug := shared.GetEnv("PASTES_DEBUG", "0")
	domain := shared.GetEnv("PASTES_DOMAIN", "pastes.sh")
	port := shared.GetEnv("PASTES_WEB_PORT", "3000")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	protocol := shared.GetEnv("PASTES_PROTOCOL", "https")
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &Config{
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

func (c *Config) StaticPath(fname string) string {
	return filepath.Join(staticDir, fname)
}

func (c *Config) SiteData() shared.SitePageData {
	return shared.SitePageData{
		Domain:  template.URL(c.Domain),
		HomeURL: template.URL("//" + c.Domain),
	}
}

func (c *Config) Static() router.Static {
	return router.Static{Dir: staticDir, Logger: c.Logger, Site: c.SiteData()}
}

func (c *Config) BlogURL(username string) string {
	return fmt.Sprintf("%s://%s.%s", c.Protocol, username, c.Domain)
}

func (c *Config) PostURL(username, slug string) string {
	return fmt.Sprintf("%s://%s.%s/%s", c.Protocol, username, c.Domain, url.PathEscape(slug))
}

func (c *Config) RawPostURL(username, slug string) string {
	return fmt.Sprintf("%s://%s.%s/raw/%s", c.Protocol, username, c.Domain, url.PathEscape(slug))
}
