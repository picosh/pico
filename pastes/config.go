package pastes

import (
	"github.com/picosh/pico/shared"
)

func NewConfigSite() *shared.ConfigSite {
	debug := shared.GetEnv("PASTES_DEBUG", "0")
	domain := shared.GetEnv("PASTES_DOMAIN", "pastes.sh")
	port := shared.GetEnv("PASTES_WEB_PORT", "3000")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	protocol := shared.GetEnv("PASTES_PROTOCOL", "https")
	storageDir := shared.GetEnv("IMGS_STORAGE_DIR", ".storage")
	minioURL := shared.GetEnv("MINIO_URL", "")
	minioUser := shared.GetEnv("MINIO_ROOT_USER", "")
	minioPass := shared.GetEnv("MINIO_ROOT_PASSWORD", "")

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
		Space:      "pastes",
		Logger:     shared.CreateLogger(),
	}
}
