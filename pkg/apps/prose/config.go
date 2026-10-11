package prose

import (
	"fmt"
	"html/template"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
)

var MAX_FILE_SIZE = 3 * shared.MB

// staticDir holds prose's templates and public files.
const staticDir = "pkg/apps/prose"

type Config struct {
	Debug              bool
	Domain             string
	Port               string
	Protocol           string
	DbURL              string
	Space              string
	AllowedExt         []string
	HiddenPosts        []string
	MaxSize            uint64
	MaxAssetSize       int64
	MaxSpecialFileSize int64
	Logger             *slog.Logger
}

func NewConfig(service string) *Config {
	debug := shared.GetEnv("PROSE_DEBUG", "0")
	domain := shared.GetEnv("PROSE_DOMAIN", "prose.sh")
	port := shared.GetEnv("PROSE_WEB_PORT", "3000")
	protocol := shared.GetEnv("PROSE_PROTOCOL", "https")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	maxSize := uint64(25 * shared.MB)
	maxImgSize := int64(10 * shared.MB)
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &Config{
		Debug:    debug == "1",
		Domain:   domain,
		Port:     port,
		Protocol: protocol,
		DbURL:    dbURL,
		Space:    "prose",
		AllowedExt: []string{
			".md",
			".lxt",
			".jpg",
			".jpeg",
			".png",
			".gif",
			".webp",
			".svg",
			".ico",
		},
		HiddenPosts:  []string{"_readme.md", "_styles.css", "_footer.md", "_404.md", "robots.txt"},
		Logger:       shared.CreateLogger(service, withPipe),
		MaxSize:      maxSize,
		MaxAssetSize: maxImgSize,
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

func (c *Config) ReadURL() string {
	return fmt.Sprintf("%s://%s", c.Protocol, c.Domain)
}

func (c *Config) CssURL() string {
	return "/_styles.css"
}
