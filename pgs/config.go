package pgs

import (
	"fmt"
	"time"

	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

var maxSize = uint64(25 * utils.MB)
var maxAssetSize = int64(10 * utils.MB)

// Needs to be small for caching files like _headers and _redirects.
var maxSpecialFileSize = int64(5 * utils.KB)

func NewConfigSite() *shared.ConfigSite {
	domain := utils.GetEnv("PGS_DOMAIN", "pgs.sh")
	port := utils.GetEnv("PGS_WEB_PORT", "3000")
	protocol := utils.GetEnv("PGS_PROTOCOL", "https")
	storageDir := utils.GetEnv("PGS_STORAGE_DIR", ".storage")
	cacheTTL, err := time.ParseDuration(utils.GetEnv("PGS_CACHE_TTL", ""))
	if err != nil {
		cacheTTL = 600 * time.Second
	}
	cacheControl := utils.GetEnv(
		"PGS_CACHE_CONTROL",
		fmt.Sprintf("max-age=%d", int(cacheTTL.Seconds())))
	minioURL := utils.GetEnv("MINIO_URL", "")
	minioUser := utils.GetEnv("MINIO_ROOT_USER", "")
	minioPass := utils.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := utils.GetEnv("DATABASE_URL", "")

	cfg := shared.ConfigSite{
		Domain:             domain,
		Port:               port,
		Protocol:           protocol,
		DbURL:              dbURL,
		StorageDir:         storageDir,
		CacheTTL:           cacheTTL,
		CacheControl:       cacheControl,
		MinioURL:           minioURL,
		MinioUser:          minioUser,
		MinioPass:          minioPass,
		Space:              "pgs",
		MaxSize:            maxSize,
		MaxAssetSize:       maxAssetSize,
		MaxSpecialFileSize: maxSpecialFileSize,
		Logger:             shared.CreateLogger("pgs"),
	}

	return &cfg
}
