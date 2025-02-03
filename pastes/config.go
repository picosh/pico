package pastes

import (
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

func NewConfigSite() *shared.ConfigSite {
	debug := utils.GetEnv("PASTES_DEBUG", "0")
	domain := utils.GetEnv("PASTES_DOMAIN", "pastes.sh")
	port := utils.GetEnv("PASTES_WEB_PORT", "3000")
	dbURL := utils.GetEnv("DATABASE_URL", "")
	protocol := utils.GetEnv("PASTES_PROTOCOL", "https")
	storageDir := utils.GetEnv("IMGS_STORAGE_DIR", ".storage")
	minioURL := utils.GetEnv("MINIO_URL", "")
	minioUser := utils.GetEnv("MINIO_ROOT_USER", "")
	minioPass := utils.GetEnv("MINIO_ROOT_PASSWORD", "")

	return &shared.ConfigSite{
		Debug:        debug == "1",
		Domain:       domain,
		Port:         port,
		Protocol:     protocol,
		DbURL:        dbURL,
		StorageDir:   storageDir,
		MinioURL:     minioURL,
		MinioUser:    minioUser,
		MinioPass:    minioPass,
		Space:        "pastes",
		Logger:       shared.CreateLogger("pastes"),
		MaxAssetSize: int64(3 * utils.MB),
	}
}
