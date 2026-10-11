package pipe

import (
	"html/template"
	"log/slog"
	"strings"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
)

// staticDir holds pipe's templates and public files.
const staticDir = "pkg/apps/pipe"

type Config struct {
	Debug  bool
	Domain string
	Port   string
	// PortOverride is the ssh port users connect to, when it isn't 22.
	PortOverride string
	Protocol     string
	DbURL        string
	Space        string
	Logger       *slog.Logger
}

func NewConfig(service string) *Config {
	domain := shared.GetEnv("PIPE_DOMAIN", "pipe.pico.sh")
	port := shared.GetEnv("PIPE_WEB_PORT", "3000")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	protocol := shared.GetEnv("PIPE_PROTOCOL", "https")
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &Config{
		Domain:   domain,
		Port:     port,
		Protocol: protocol,
		DbURL:    dbURL,
		Logger:   shared.CreateLogger(service, withPipe),
		Space:    "pipe",
	}
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
