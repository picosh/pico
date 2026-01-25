package storage

import (
	"fmt"
	"log/slog"

	"github.com/picosh/pico/pkg/shared"
)

func GetStorageTypeFromEnv() string {
	return shared.GetEnv("STORAGE_ADAPTER", "fs")
}

func NewStorage(logger *slog.Logger, adapter string) (StorageServe, error) {
	logger.Info("storage adapter", "adapter", adapter)
	switch adapter {
	case "fs":
		fsPath := shared.GetEnv("FS_STORAGE_DIR", "/tmp/pico_storage")
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
