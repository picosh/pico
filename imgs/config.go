package imgs

import (
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

func NewConfigSite() *shared.ConfigSite {
	debug := utils.GetEnv("IMGS_DEBUG", "0")
	domain := utils.GetEnv("IMGS_DOMAIN", "prose.sh")
	port := utils.GetEnv("IMGS_WEB_PORT", "3000")
	protocol := utils.GetEnv("IMGS_PROTOCOL", "https")
	storageDir := utils.GetEnv("IMGS_STORAGE_DIR", ".storage")
	minioURL := utils.GetEnv("MINIO_URL", "")
	minioUser := utils.GetEnv("MINIO_ROOT_USER", "")
	minioPass := utils.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := utils.GetEnv("DATABASE_URL", "")

	cfg := shared.ConfigSite{
		Debug:      debug == "1",
		Domain:     domain,
		Port:       port,
		Protocol:   protocol,
		DbURL:      dbURL,
		StorageDir: storageDir,
		MinioURL:   minioURL,
		MinioUser:  minioUser,
		MinioPass:  minioPass,
		Space:      "imgs",
		AllowedExt: []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".ico"},
		Logger:     shared.CreateLogger("imgs"),
	}

	return &cfg
}
