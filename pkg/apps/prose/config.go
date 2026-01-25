package prose

import (
	"strings"

	"github.com/picosh/pico/pkg/shared"
)

var MAX_FILE_SIZE = 3 * shared.MB

func NewConfigSite(service string) *shared.ConfigSite {
	debug := shared.GetEnv("PROSE_DEBUG", "0")
	domain := shared.GetEnv("PROSE_DOMAIN", "prose.sh")
	port := shared.GetEnv("PROSE_WEB_PORT", "3000")
	protocol := shared.GetEnv("PROSE_PROTOCOL", "https")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	maxSize := uint64(25 * shared.MB)
	maxImgSize := int64(10 * shared.MB)
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &shared.ConfigSite{
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
