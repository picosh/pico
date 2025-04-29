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

		return NewStorageMinio(logger, minioURL, minioUser, minioPass)
	case "garage":
		garageURL := utils.GetEnv("GARAGE_URL", "")
		garageUser := utils.GetEnv("GARAGE_ROOT_USER", "")
		garagePass := utils.GetEnv("GARAGE_ROOT_PASSWORD", "")
		garageToken := utils.GetEnv("GARAGE_TOKEN", "")

		return NewStorageGarage(logger, garageURL, garageUser, garagePass, garageToken)
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", storageType)
	}
}
