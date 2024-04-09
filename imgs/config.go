package imgs

import (
	"github.com/picosh/pico/shared"
)

func NewConfigSite() *shared.ConfigSite {
	debug := shared.GetEnv("IMGS_DEBUG", "0")
	domain := shared.GetEnv("IMGS_DOMAIN", "prose.sh")
	port := shared.GetEnv("IMGS_WEB_PORT", "3000")
	protocol := shared.GetEnv("IMGS_PROTOCOL", "https")
	storageDir := shared.GetEnv("IMGS_STORAGE_DIR", ".storage")
	minioURL := shared.GetEnv("MINIO_URL", "")
	minioUser := shared.GetEnv("MINIO_ROOT_USER", "")
	minioPass := shared.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := shared.GetEnv("DATABASE_URL", "")

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
		AllowedExt:    []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".ico"},
		Logger:     shared.CreateLogger(debug == "1"),
	}

	return &cfg
}
