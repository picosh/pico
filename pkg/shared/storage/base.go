package storage

import (
	"fmt"
	"log/slog"

	"github.com/picosh/utils"
)

func GetStorageTypeFromEnv() string {
	return utils.GetEnv("STORAGE_ADAPTER", "minio")
}

func NewStorage(logger *slog.Logger, adapter string) (StorageServe, error) {
	logger.Info("storage adapter", "adapter", adapter)
	switch adapter {
	case "minio":
		minioURL := utils.GetEnv("MINIO_URL", "")
		minioUser := utils.GetEnv("MINIO_ROOT_USER", "")
		minioPass := utils.GetEnv("MINIO_ROOT_PASSWORD", "")
		logger.Info("using minio storage", "url", minioURL, "user", minioUser)
		return NewStorageMinio(logger, minioURL, minioUser, minioPass)
	case "fs":
		fsPath := utils.GetEnv("FS_STORAGE_DIR", "/tmp/pico_storage")
		logger.Info("using filesystem storage", "path", fsPath)
		return NewStorageFS(logger, fsPath)
	case "memory":
		data := map[string]map[string]string{}
		logger.Info("using memory storage")
		return NewStorageMemory(data)
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", adapter)
	}
}
