package upload

import (
	"fmt"
	"strings"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/shared"
)

func (h *UploadImgHandler) validateImg(data *filehandlers.PostMetaData) (bool, error) {
	if !h.DBPool.HasFeatureForUser(data.User.ID, "imgs") {
		return false, fmt.Errorf("ERROR: user (%s) does not have access to this feature (imgs)", data.User.Name)
	}

	fileSize, err := h.DBPool.FindTotalSizeForUser(data.User.ID)
	if err != nil {
		return false, err
	}
	if fileSize+int(data.FileSize) > maxSize {
		return false, fmt.Errorf("ERROR: user (%s) has exceeded (%d) max (%d)", data.User.Name, fileSize, maxSize)
	}

	if !shared.IsExtAllowed(data.Filename, h.Cfg.AllowedExt) {
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

func (h *UploadImgHandler) metaImg(data *filehandlers.PostMetaData) error {
	// create or get
	bucket, err := h.Storage.UpsertBucket(data.User.ID)
	if err != nil {
		return err
	}
	fname, err := h.Storage.PutFile(bucket, data.Filename, []byte(data.Text))
	if err != nil {
		return err
	}

	data.Data = db.PostData{
		ImgPath: fname,
	}

	if data.Cur != nil {
		data.Text = data.Cur.Text
		data.Title = data.Cur.Title
		data.PublishAt = data.Cur.PublishAt
		data.Description = data.Cur.Description
	}

	return nil
}

func (h *UploadImgHandler) writeImg(data *filehandlers.PostMetaData) error {
	valid, err := h.validateImg(data)
	if !valid {
		return err
	}

	err = h.metaImg(data)
	if err != nil {
		h.Cfg.Logger.Info(err)
		return err
	}

	if len(data.Text) == 0 {
		err = h.removePost(data)
		if err != nil {
			return err
		}

		bucket, err := h.Storage.UpsertBucket(data.User.ID)
		if err != nil {
			return err
		}
		err = h.Storage.DeleteFile(bucket, data.Filename)
		if err != nil {
			return err
		}
	} else if data.Cur == nil {
		h.Cfg.Logger.Infof("(%s) not found, adding record", data.Filename)
		insertPost := db.Post{
			UserID: h.User.ID,
			Space:  h.Cfg.Space,

			Data:      data.Data,
			Filename:  data.Filename,
			FileSize:  data.FileSize,
			Hidden:    data.Hidden,
			MimeType:  data.MimeType,
			PublishAt: data.PublishAt,
			Shasum:    data.Shasum,
			Slug:      data.Slug,
		}
		_, err := h.DBPool.InsertPost(&insertPost)
		if err != nil {
			h.Cfg.Logger.Errorf("error for %s: %v", data.Filename, err)
			return fmt.Errorf("error for %s: %v", data.Filename, err)
		}
	} else {
		if shared.Shasum([]byte(data.Text)) == data.Cur.Shasum {
			h.Cfg.Logger.Infof("(%s) found, but text is identical, skipping", data.Filename)
			return nil
		}

		h.Cfg.Logger.Infof("(%s) found, updating record", data.Filename)
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
		}
		_, err = h.DBPool.UpdatePost(&updatePost)
		if err != nil {
			h.Cfg.Logger.Errorf("error for %s: %v", data.Filename, err)
			return fmt.Errorf("error for %s: %v", data.Filename, err)
		}

		h.Cfg.Logger.Infof(
			"Found (%s) post tags, replacing with old tags",
			strings.Join(data.Tags, ","),
		)
		err = h.DBPool.ReplaceTagsForPost(data.Tags, data.Cur.ID)
		if err != nil {
			h.Cfg.Logger.Errorf("error for %s: %v", data.Filename, err)
			return fmt.Errorf("error for %s: %v", data.Filename, err)
		}
	}

	return nil
}
