package pgs

import (
	"github.com/picosh/pico/shared"
)

var maxSize = uint64(25 * shared.MB)
var maxAssetSize = int64(10 * shared.MB)

func NewConfigSite() *shared.ConfigSite {
	debug := shared.GetEnv("PGS_DEBUG", "0")
	domain := shared.GetEnv("PGS_DOMAIN", "pgs.sh")
	port := shared.GetEnv("PGS_WEB_PORT", "3000")
	protocol := shared.GetEnv("PGS_PROTOCOL", "https")
	storageDir := shared.GetEnv("PGS_STORAGE_DIR", ".storage")
	minioURL := shared.GetEnv("MINIO_URL", "")
	minioUser := shared.GetEnv("MINIO_ROOT_USER", "")
	minioPass := shared.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	secret := shared.GetEnv("PICO_SECRET", "")
	if secret == "" {
		panic("must provide PICO_SECRET environment variable")
	}

	cfg := shared.ConfigSite{
		Debug:        debug == "1",
		Secret:       secret,
		Domain:       domain,
		Port:         port,
		Protocol:     protocol,
		DbURL:        dbURL,
		StorageDir:   storageDir,
		MinioURL:     minioURL,
		MinioUser:    minioUser,
		MinioPass:    minioPass,
		Space:        "pgs",
		MaxSize:      maxSize,
		MaxAssetSize: maxAssetSize,
		Logger:       shared.CreateLogger(debug == "1"),
	}

	return &cfg
}
