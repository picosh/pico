package cache

import (
	"log/slog"
	"time"

	"github.com/picosh/utils"
)

var CacheTimeout time.Duration

func init() {
	cacheDuration := utils.GetEnv("STORAGE_MINIO_CACHE_DURATION", "1m")
	duration, err := time.ParseDuration(cacheDuration)
	if err != nil {
		slog.Error("Invalid STORAGE_MINIO_CACHE_DURATION value, using default 1m", "error", err)
		duration = 1 * time.Minute
	}

	CacheTimeout = duration
}
