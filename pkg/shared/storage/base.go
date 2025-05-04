package storage

import (
	"fmt"
	"log/slog"

	"github.com/picosh/utils"
)

func NewStorage(logger *slog.Logger) (StorageServe, error) {
	storageType := utils.GetEnv("STORAGE_TYPE", "minio")

	switch storageType {
	case "minio":
		minioURL := utils.GetEnv("MINIO_URL", "")
		minioUser := utils.GetEnv("MINIO_ROOT_USER", "")
		minioPass := utils.GetEnv("MINIO_ROOT_PASSWORD", "")

		logger.Info("Using minio storage", "url", minioURL, "user", minioUser)

		return NewStorageMinio(logger, minioURL, minioUser, minioPass)
	case "garage":
		garageURL := utils.GetEnv("GARAGE_URL", "")
		garageUser := utils.GetEnv("GARAGE_ROOT_USER", "")
		garagePass := utils.GetEnv("GARAGE_ROOT_PASSWORD", "")
		garageAdminURL := utils.GetEnv("GARAGE_ADMIN_URL", "")
		garageAdminToken := utils.GetEnv("GARAGE_ADMIN_TOKEN", "")

		logger.Info("Using garage storage", "url", garageURL, "user", garageUser)

		return NewStorageGarage(logger, garageURL, garageUser, garagePass, garageAdminURL, garageAdminToken)
	case "fs":
		fsPath := utils.GetEnv("FS_STORAGE_DIR", "/tmp/pico_storage")

		logger.Info("Using filesystem storage", "path", fsPath)

		return NewStorageFS(logger, fsPath)
	case "memory":
		data := map[string]map[string]string{}

		logger.Info("Using memory storage")

		return NewStorageMemory(data)
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", storageType)
	}
}
