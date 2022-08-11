package upload

import (
	"fmt"
	"strings"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/prose"
	"git.sr.ht/~erock/pico/shared"
)

func (h *UploadImgHandler) validateMd(data *filehandlers.PostMetaData) (bool, error) {
	if !shared.IsTextFile(data.Text) {
		err := fmt.Errorf(
			"WARNING: (%s) invalid file must be plain text (utf-8), skipping",
			data.Filename,
		)
		return false, err
	}

	if !shared.IsExtAllowed(data.Filename, []string{".md"}) {
		err := fmt.Errorf(
			"(%s) invalid file, format must be (.md), skipping",
			data.Filename,
		)
		return false, err
	}

	return true, nil
}

func (h *UploadImgHandler) metaMd(data *filehandlers.PostMetaData) error {
	hooks := prose.MarkdownHooks{Cfg: h.Cfg}
	err := hooks.FileMeta(data)
	if err != nil {
		return err
	}

	if data.Cur != nil {
		data.Filename = data.Cur.Filename
		data.FileSize = data.Cur.FileSize
		data.MimeType = data.Cur.MimeType
		data.Data = data.Cur.Data
		data.Shasum = data.Cur.Shasum
		data.Slug = data.Cur.Slug
	}

	if data.Description == "" {
		data.Description = data.Title
	}

	return nil
}

func (h *UploadImgHandler) writeMd(data *filehandlers.PostMetaData) error {
	valid, err := h.validateMd(data)
	if !valid {
		return err
	}

	err = h.metaMd(data)
	if err != nil {
		return err
	}

	if len(data.Text) == 0 {
		err = h.removePost(data)
		if err != nil {
			return err
		}
	} else if data.Cur == nil {
		h.Cfg.Logger.Infof("(%s) not found, adding record", data.Filename)
		insertPost := db.Post{
			UserID: h.User.ID,
			Space:  h.Cfg.Space,

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
		}
		post, err := h.DBPool.InsertPost(&insertPost)
		if err != nil {
			h.Cfg.Logger.Errorf("error for %s: %v", data.Filename, err)
			return fmt.Errorf("error for %s: %v", data.Filename, err)
		}

		if len(data.Tags) > 0 {
			h.Cfg.Logger.Infof(
				"Found (%s) post tags, replacing with old tags",
				strings.Join(data.Tags, ","),
			)
			err = h.DBPool.ReplaceTagsForPost(data.Tags, post.ID)
			if err != nil {
				h.Cfg.Logger.Errorf("error for %s: %v", data.Filename, err)
				return fmt.Errorf("error for %s: %v", data.Filename, err)
			}
		}
	} else {
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
