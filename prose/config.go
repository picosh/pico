package prose

import (
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

func NewConfigSite() *shared.ConfigSite {
	debug := utils.GetEnv("PROSE_DEBUG", "0")
	domain := utils.GetEnv("PROSE_DOMAIN", "prose.sh")
	port := utils.GetEnv("PROSE_WEB_PORT", "3000")
	protocol := utils.GetEnv("PROSE_PROTOCOL", "https")
	storageDir := utils.GetEnv("PROSE_STORAGE_DIR", ".storage")
	minioURL := utils.GetEnv("MINIO_URL", "")
	minioUser := utils.GetEnv("MINIO_ROOT_USER", "")
	minioPass := utils.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := utils.GetEnv("DATABASE_URL", "")
	maxSize := uint64(500 * utils.MB)
	maxImgSize := int64(10 * utils.MB)

	return &shared.ConfigSite{
		Debug:      debug == "1",
		Domain:     domain,
		Port:       port,
		Protocol:   protocol,
		DbURL:      dbURL,
		StorageDir: storageDir,
		MinioURL:   minioURL,
		MinioUser:  minioUser,
		MinioPass:  minioPass,
		Space:      "prose",
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
		Logger:       shared.CreateLogger("prose"),
		MaxSize:      maxSize,
		MaxAssetSize: maxImgSize,
	}
}
