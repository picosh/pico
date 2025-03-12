package pobj

import (
	"log/slog"
	"os"

	"github.com/picosh/pico/pkg/pobj/storage"
)

func GetEnv(key string, defaultVal string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultVal
}

func EnvDriverDetector(logger *slog.Logger) (storage.ObjectStorage, error) {
	driver := GetEnv("OBJECT_DRIVER", "fs")
	logger.Info("driver detected", "driver", driver)

	if driver == "memory" {
		return storage.NewStorageMemory(map[string]map[string]string{})
	} else if driver == "minio" {
		url := GetEnv("MINIO_URL", "")
		user := GetEnv("MINIO_ROOT_USER", "")
		pass := GetEnv("MINIO_ROOT_PASSWORD", "")
		logger.Info(
			"object config detected",
			"url", url,
			"user", user,
		)
		return storage.NewStorageMinio(url, user, pass)
	} else if driver == "s3" {
		region := GetEnv("AWS_REGION", "us-east-1")
		key := GetEnv("AWS_ACCESS_KEY_ID", "")
		secret := GetEnv("AWS_SECRET_ACCESS_KEY", "")
		return storage.NewStorageS3(region, key, secret)
	}

	// implied driver == "fs"
	storageDir := GetEnv("OBJECT_URL", "./.storage")
	logger.Info("object config detected", "dir", storageDir)
	return storage.NewStorageFS(logger, storageDir)
}
