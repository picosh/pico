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
	fname := filepath.Base(data.Filepath)
	if int(data.Size) > h.Cfg.MaxAssetSize {
		return false, fmt.Errorf("ERROR: file (%s) has exceeded maximum file size (%d bytes)", fname, h.Cfg.MaxAssetSize)
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

	assetFilename := shared.GetAssetFileName(data.FileEntry)

	if data.Size == 0 {
		err = h.Storage.DeleteFile(data.Bucket, assetFilename)
		if err != nil {
			return err
		}
	} else {
		reader := bytes.NewReader(data.Text)
		h.Cfg.Logger.Infof(
			"(%s) uploading to (bucket: %s) (%s)",
			data.User.Name,
			data.Bucket.Name,
			assetFilename,
		)
		_, err := h.Storage.PutFile(
			data.Bucket,
			assetFilename,
			storage.NopReaderAtCloser(reader),
		)
		if err != nil {
			return err
		}
	}

	return nil
}
