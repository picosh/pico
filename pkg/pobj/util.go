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

	switch driver {
	case "memory":
		return storage.NewStorageMemory(map[string]map[string]string{})
	case "minio":
		url := GetEnv("MINIO_URL", "")
		user := GetEnv("MINIO_ROOT_USER", "")
		pass := GetEnv("MINIO_ROOT_PASSWORD", "")
		logger.Info(
			"object config detected",
			"url", url,
			"user", user,
		)
		return storage.NewStorageMinio(logger, url, user, pass)
	}

	// implied driver == "fs"
	storageDir := GetEnv("OBJECT_URL", "./.storage")
	logger.Info("object config detected", "dir", storageDir)
	return storage.NewStorageFS(logger, storageDir)
}
