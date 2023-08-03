package uploadassets

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
)

func (h *UploadAssetHandler) validateAsset(data *FileData) (bool, error) {
	assetBucket := shared.GetAssetBucketName(data.User.ID)
	bucket, err := h.Storage.UpsertBucket(assetBucket)
	if err != nil {
		return false, err
	}
	totalFileSize, err := h.Storage.GetBucketQuota(bucket)
	if err != nil {
		return false, err
	}

	fname := filepath.Base(data.Filepath)
	if int(data.Size) > maxAssetSize {
		return false, fmt.Errorf("ERROR: file (%s) has exceeded maximum file size (%d bytes)", fname, maxAssetSize)
	}

	if totalFileSize+uint64(data.Size) > uint64(maxSize) {
		return false, fmt.Errorf("ERROR: user (%s) has exceeded (%d bytes) max (%d bytes)", data.User.Name, totalFileSize, maxSize)
	}

	if !shared.IsExtAllowed(fname, h.Cfg.AllowedExt) {
		extStr := strings.Join(h.Cfg.AllowedExt, ",")
		err := fmt.Errorf(
			"ERROR: (%s) invalid file, format must be (%s), skipping",
			fname,
			extStr,
		)
		return false, err
	}

	return true, nil
}

func (h *UploadAssetHandler) writeAsset(data *FileData) error {
	valid, err := h.validateAsset(data)
	if !valid {
		return err
	}

	assetBucket := shared.GetAssetBucketName(data.User.ID)
	assetFilename := shared.GetAssetFileName(data.FileEntry)
	bucket, err := h.Storage.UpsertBucket(assetBucket)
	if err != nil {
		return err
	}

	if data.Size == 0 {
		err = h.Storage.DeleteFile(bucket, assetFilename)
		if err != nil {
			return err
		}
	} else {
		reader := bytes.NewReader(data.Text)
		_, err := h.Storage.PutFile(
			bucket,
			assetFilename,
			storage.NopReaderAtCloser(reader),
		)
		if err != nil {
			return err
		}
	}

	return nil
}
