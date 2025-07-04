package prose

import (
	"strings"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils"
)

var MAX_FILE_SIZE = 3 * utils.MB

func NewConfigSite(service string) *shared.ConfigSite {
	debug := utils.GetEnv("PROSE_DEBUG", "0")
	domain := utils.GetEnv("PROSE_DOMAIN", "prose.sh")
	port := utils.GetEnv("PROSE_WEB_PORT", "3000")
	protocol := utils.GetEnv("PROSE_PROTOCOL", "https")
	dbURL := utils.GetEnv("DATABASE_URL", "")
	maxSize := uint64(25 * utils.MB)
	maxImgSize := int64(10 * utils.MB)
	withPipe := strings.ToLower(utils.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &shared.ConfigSite{
		Debug:    debug == "1",
		Domain:   domain,
		Port:     port,
		Protocol: protocol,
		DbURL:    dbURL,
		Space:    "prose",
		AllowedExt: []string{
			".md",
			".jpg",
			".jpeg",
			".png",
			".gif",
			".webp",
			".svg",
			".ico",
		},
		HiddenPosts:  []string{"_readme.md", "_styles.css", "_footer.md", "_404.md"},
		Logger:       shared.CreateLogger(service, withPipe),
		MaxSize:      maxSize,
		MaxAssetSize: maxImgSize,
	}
}
