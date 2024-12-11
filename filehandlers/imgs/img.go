package uploadimgs

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	sendutils "github.com/picosh/send/utils"
	"github.com/picosh/utils"
)

func (h *UploadImgHandler) validateImg(data *PostMetaData) (bool, error) {
	totalFileSize, err := h.DBPool.FindTotalSizeForUser(data.User.ID)
	if err != nil {
		return false, err
	}

	fileMax := data.FeatureFlag.Data.FileMax
	if int64(data.FileSize) > fileMax {
		return false, fmt.Errorf("ERROR: file (%s) has exceeded maximum file size (%d bytes)", data.Filename, fileMax)
	}

	storageMax := data.FeatureFlag.Data.StorageMax
	if uint64(totalFileSize+data.FileSize) > storageMax {
		return false, fmt.Errorf("ERROR: user (%s) has exceeded (%d bytes) max (%d bytes)", data.User.Name, totalFileSize, storageMax)
	}

	if !utils.IsExtAllowed(data.Filepath, h.Cfg.AllowedExt) {
		extStr := strings.Join(h.Cfg.AllowedExt, ",")
		err := fmt.Errorf(
			"ERROR: (%s) invalid file, format must be (%s), skipping",
			data.Filename,
			extStr,
		)
		return false, err
	}

	return true, nil
}

func (h *UploadImgHandler) metaImg(data *PostMetaData) error {
	// if the file is empty that means we should delete it
	// so we can skip all the meta info
	if data.FileSize == 0 {
		return nil
	}

	bucket, err := h.Storage.UpsertBucket(data.User.ID)
	if err != nil {
		return err
	}

	reader := bytes.NewReader([]byte(data.Text))

	fname, _, err := h.Storage.PutObject(
		bucket,
		data.Filename,
		sendutils.NopReaderAtCloser(reader),
		&sendutils.FileEntry{},
	)
	if err != nil {
		return err
	}

	data.Data = db.PostData{
		ImgPath: fname,
	}

	data.Text = ""

	return nil
}

func (h *UploadImgHandler) writeImg(s ssh.Session, data *PostMetaData) error {
	valid, err := h.validateImg(data)
	if !valid {
		return err
	}
	user, err := h.DBPool.FindUser(s.Permissions().Extensions["user_id"])
	if err != nil {
		return err
	}

	logger := h.Cfg.Logger
	logger = shared.LoggerWithUser(logger, user)

	err = h.metaImg(data)
	if err != nil {
		logger.Error("could not get meta for img", "err", err.Error())
		return err
	}

	modTime := time.Now()

	if data.Mtime > 0 {
		modTime = time.Unix(data.Mtime, 0)
	}

	logger = logger.With(
		"filename", data.Filename,
	)

	if data.Cur == nil {
		logger.Info("file not found, adding record")
		insertPost := db.Post{
			UserID: user.ID,
			Space:  Space,

			Data:        data.Data,
			Description: data.Description,
			Filename:    data.Filename,
			FileSize:    data.FileSize,
			Hidden:      data.Hidden,
			MimeType:    data.MimeType,
			PublishAt:   data.PublishAt,
			Shasum:      data.Shasum,
			Slug:        data.Slug,
			Text:        data.Text,
			Title:       data.Title,
			UpdatedAt:   &modTime,
		}
		_, err := h.DBPool.InsertPost(&insertPost)
		if err != nil {
			logger.Error("post could not create", "err", err.Error())
			return fmt.Errorf("error for %s: %v", data.Filename, err)
		}

		if len(data.Tags) > 0 {
			logger.Info(
				"found post tags, replacing with old tags",
				"tags", strings.Join(data.Tags, ","),
			)
			err = h.DBPool.ReplaceTagsForPost(data.Tags, data.Post.ID)
			if err != nil {
				logger.Error("post could not replace tags", "err", err.Error())
				return fmt.Errorf("error for %s: %v", data.Filename, err)
			}
		}
	} else {
		if data.Shasum == data.Cur.Shasum && modTime.Equal(*data.Cur.UpdatedAt) {
			logger.Info("image found, but image is identical, skipping")
			return nil
		}

		logger.Info("file found, updating record")

		updatePost := db.Post{
			ID: data.Cur.ID,

			Data:        data.Data,
			FileSize:    data.FileSize,
			Description: data.Description,
			PublishAt:   data.PublishAt,
			Slug:        data.Slug,
			Shasum:      data.Shasum,
			Text:        data.Text,
			Title:       data.Title,
			Hidden:      data.Hidden,
			UpdatedAt:   &modTime,
		}
		_, err = h.DBPool.UpdatePost(&updatePost)
		if err != nil {
			logger.Error("post could not update", "err", err.Error())
			return fmt.Errorf("error for %s: %v", data.Filename, err)
		}

		logger.Info(
			"found post tags, replacing with old tags",
			"tags", strings.Join(data.Tags, ","),
		)
		err = h.DBPool.ReplaceTagsForPost(data.Tags, data.Cur.ID)
		if err != nil {
			logger.Error("post could not replace tags", "err", err.Error())
			return fmt.Errorf("error for %s: %v", data.Filename, err)
		}
	}

	return nil
}
