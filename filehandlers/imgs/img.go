package uploadimgs

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/gliderlabs/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/imgs/storage"
	"github.com/picosh/pico/shared"
)

func (h *UploadImgHandler) validateImg(data *PostMetaData) (bool, error) {
	totalFileSize, err := h.DBPool.FindTotalSizeForUser(data.User.ID)
	if err != nil {
		return false, err
	}

	if data.FileSize > maxImgSize {
		return false, fmt.Errorf("ERROR: file (%s) has exceeded maximum file size (%d bytes)", data.Filename, maxImgSize)
	}

	if totalFileSize+data.FileSize > maxSize {
		return false, fmt.Errorf("ERROR: user (%s) has exceeded (%d bytes) max (%d bytes)", data.User.Name, totalFileSize, maxSize)
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
	tee := bytes.NewReader([]byte(data.Text))

	fname, err := h.Storage.PutFile(
		bucket,
		data.Filename,
		storage.NopReaderAtCloser(reader),
	)
	if err != nil {
		return err
	}

	opt := shared.NewImgOptimizer(h.Cfg.Logger, "")
	// for small images we want to preserve quality
	// since it can have a dramatic effect
	if data.FileSize < 3*MB {
		opt.Quality = 100
		opt.Lossless = true
	} else {
		opt.Quality = 80
		opt.Lossless = false
	}

	contents := &bytes.Buffer{}
	img, err := opt.GetImage(tee, data.MimeType)
	if err != nil {
		return err
	}

	err = opt.EncodeWebp(contents, img)
	if err != nil {
		return err
	}

	webpReader := bytes.NewReader(contents.Bytes())
	_, err = h.Storage.PutFile(
		bucket,
		fmt.Sprintf("%s.webp", shared.SanitizeFileExt(data.Filename)),
		storage.NopReaderAtCloser(webpReader),
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
	user, err := getUser(s)
	if err != nil {
		return err
	}

	err = h.metaImg(data)
	if err != nil {
		h.Cfg.Logger.Info(err)
		return err
	}

	if len(data.OrigText) == 0 {
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
		webp := fmt.Sprintf("%s.webp", shared.SanitizeFileExt(data.Filename))
		err = h.Storage.DeleteFile(bucket, webp)
		if err != nil {
			return err
		}
	} else if data.Cur == nil {
		h.Cfg.Logger.Infof("(%s) not found, adding record", data.Filename)
		insertPost := db.Post{
			UserID: user.ID,
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
		_, err := h.DBPool.InsertPost(&insertPost)
		if err != nil {
			h.Cfg.Logger.Errorf("error for %s: %v", data.Filename, err)
			return fmt.Errorf("error for %s: %v", data.Filename, err)
		}

		if len(data.Tags) > 0 {
			h.Cfg.Logger.Infof(
				"Found (%s) post tags, replacing with old tags",
				strings.Join(data.Tags, ","),
			)
			err = h.DBPool.ReplaceTagsForPost(data.Tags, data.Post.ID)
			if err != nil {
				h.Cfg.Logger.Errorf("error for %s: %v", data.Filename, err)
				return fmt.Errorf("error for %s: %v", data.Filename, err)
			}
		}
	} else {
		if data.Shasum == data.Cur.Shasum {
			h.Cfg.Logger.Infof("(%s) found, but image is identical, skipping", data.Filename)
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
