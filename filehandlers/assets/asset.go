package uploadassets

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/send/utils"
)

func (h *UploadAssetHandler) validateAsset(data *FileData) (bool, error) {
	if data.BucketQuota >= uint64(h.Cfg.MaxSize) {
		return false, fmt.Errorf(
			"ERROR: user (%s) has exceeded (%d bytes) max (%d bytes)",
			data.User.Name,
			data.BucketQuota,
			h.Cfg.MaxSize,
		)
	}

	projectName := shared.GetProjectName(data.FileEntry)
	if projectName == "" || projectName == "/" || projectName == "." {
		return false, fmt.Errorf("ERROR: invalid project name, you must copy files to a non-root folder (e.g. pgs.sh:/project-name)")
	}

	fname := filepath.Base(data.Filepath)
	if int(data.Size) > h.Cfg.MaxAssetSize {
		return false, fmt.Errorf("ERROR: file (%s) has exceeded maximum file size (%d bytes)", fname, h.Cfg.MaxAssetSize)
	}

	// ".well-known" is a special case
	if strings.Contains(fname, "/.well-known/") {
		if shared.IsTextFile(string(data.Text)) {
			return true, nil
		} else {
			return false, fmt.Errorf("(%s) not a utf-8 text file", data.Filepath)
		}
	}

	// special file we use for custom routing
	if fname == "_redirects" {
		return true, nil
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
			utils.NopReaderAtCloser(reader),
			data.FileEntry,
		)
		if err != nil {
			return err
		}
	}

	return nil
}
