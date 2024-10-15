package pgs

import (
	"strconv"

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
	minioURL := utils.GetEnv("MINIO_URL", "")
	minioUser := utils.GetEnv("MINIO_ROOT_USER", "")
	minioPass := utils.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := utils.GetEnv("DATABASE_URL", "")
	secret := utils.GetEnv("PICO_SECRET", "")
	cacheSizeStr := utils.GetEnv("PGS_CACHE_SIZE", "4096")
	cacheSize, err := strconv.Atoi(cacheSizeStr)
	if err != nil {
		panic(err)
	}
	cacheExpireStr := utils.GetEnv("PGS_CACHE_EXPIRE_SECONDS", "3600")
	cacheExpireSeconds, err := strconv.Atoi(cacheExpireStr)
	if err != nil {
		panic(err)
	}
	if secret == "" {
		panic("must provide PICO_SECRET environment variable")
	}

	cfg := shared.ConfigSite{
		Secret:             secret,
		Domain:             domain,
		Port:               port,
		Protocol:           protocol,
		DbURL:              dbURL,
		StorageDir:         storageDir,
		MinioURL:           minioURL,
		MinioUser:          minioUser,
		MinioPass:          minioPass,
		Space:              "pgs",
		MaxSize:            maxSize,
		MaxAssetSize:       maxAssetSize,
		MaxSpecialFileSize: maxSpecialFileSize,
		CacheSize:          cacheSize,
		CacheExpireSeconds: cacheExpireSeconds,
		Logger:             shared.CreateLogger("pgs"),
	}

	return &cfg
}
