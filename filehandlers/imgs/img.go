package uploadimgs

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/send/utils"
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

	if !shared.IsExtAllowed(data.Filepath, h.Cfg.AllowedExt) {
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

	// we want to keep the original file so people can use that
	// if our webp optimizer doesn't work properly
	fname, err := h.Storage.PutFile(
		bucket,
		data.Filename,
		utils.NopReaderAtCloser(reader),
		&utils.FileEntry{},
	)
	if err != nil {
		return err
	}

	opt := shared.NewImgOptimizer(h.Cfg.Logger, "")
	// for small images we want to preserve quality
	// since it can have a dramatic effect
	if data.FileSize < 3*shared.MB {
		opt.Quality = 100
		opt.Lossless = true
	} else {
		opt.Quality = 80
		opt.Lossless = false
	}

	var webpReader *bytes.Reader
	contents := &bytes.Buffer{}

	img, err := shared.GetImageForOptimization(tee, data.MimeType)
	finalName := shared.SanitizeFileExt(data.Filename)
	if errors.Is(err, shared.ErrAlreadyWebPError) {
		h.Cfg.Logger.Infof("(%s) is already webp, skipping encoding", data.Filename)
		finalName = fmt.Sprintf("%s.webp", finalName)
		webpReader = tee
	} else if err != nil {
		h.Cfg.Logger.Infof("(%s) is a file format (%s) that we cannot convert to webp, skipping encoding", data.Filename, data.MimeType)
		webpReader = tee
	} else {
		err = opt.EncodeWebp(contents, img)
		if err != nil {
			return err
		}

		finalName = fmt.Sprintf("%s.webp", finalName)
		webpReader = bytes.NewReader(contents.Bytes())
	}

	if webpReader == nil {
		return fmt.Errorf("contents of webp file is nil")
	}

	_, err = h.Storage.PutFile(
		bucket,
		finalName,
		utils.NopReaderAtCloser(webpReader),
		&utils.FileEntry{},
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

	modTime := time.Unix(data.Mtime, 0)

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
			UpdatedAt:   &modTime,
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
		if data.Shasum == data.Cur.Shasum && modTime.Equal(*data.Cur.UpdatedAt) {
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
			Hidden:      data.Hidden,
			UpdatedAt:   &modTime,
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
