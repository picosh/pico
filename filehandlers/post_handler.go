package filehandlers

import (
	"fmt"
	"io"
	"time"

	"git.sr.ht/~erock/pico/shared"
	"git.sr.ht/~erock/pico/wish/cms/db"
	"git.sr.ht/~erock/pico/wish/cms/util"
	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/gliderlabs/ssh"
)

type PostMetaData struct {
	Filename    string
	Slug        string
	Text        string
	Title       string
	Description string
	PublishAt   *time.Time
	Hidden      bool
}

type ScpFileHooks interface {
	FileValidate(text string, filename string) (bool, error)
	FileMeta(text string, data *PostMetaData) error
}

type ScpUploadHandler struct {
	User   *db.User
	DBPool db.DB
	Cfg    *shared.ConfigSite
	Hooks  ScpFileHooks
}

func NewScpPostHandler(dbpool db.DB, cfg *shared.ConfigSite, hooks ScpFileHooks) *ScpUploadHandler {
	return &ScpUploadHandler{
		DBPool: dbpool,
		Cfg:    cfg,
		Hooks:  hooks,
	}
}

func (h *ScpUploadHandler) Validate(s ssh.Session) error {
	var err error
	key, err := util.KeyText(s)
	if err != nil {
		return fmt.Errorf("key not found")
	}

	user, err := h.DBPool.FindUserForKey(s.User(), key)
	if err != nil {
		return err
	}

	if user.Name == "" {
		return fmt.Errorf("must have username set")
	}

	h.User = user
	return nil
}

func (h *ScpUploadHandler) Write(s ssh.Session, entry *utils.FileEntry) (string, error) {
	logger := h.Cfg.Logger
	userID := h.User.ID
	filename := entry.Name

	user, err := h.DBPool.FindUser(userID)
	if err != nil {
		return "", fmt.Errorf("error for %s: %v", filename, err)
	}

	var text string
	if b, err := io.ReadAll(entry.Reader); err == nil {
		text = string(b)
	}

	valid, err := h.Hooks.FileValidate(text, entry.Filepath)
	if !valid {
		return "", err
	}

	post, err := h.DBPool.FindPostWithFilename(filename, userID, h.Cfg.Space)
	if err != nil {
		logger.Debugf("unable to load post (%s), continuing", filename)
		logger.Debug(err)
	}

	now := time.Now()
	slug := shared.SanitizeFileExt(filename)
	metadata := PostMetaData{
		Filename:  filename,
		Slug:      slug,
		Title:     shared.ToUpper(slug),
		PublishAt: &now,
	}
	if post != nil {
		metadata.PublishAt = post.PublishAt
	}

	err = h.Hooks.FileMeta(text, &metadata)
	if err != nil {
		logger.Error(err)
		return "", err
	}

	// if the file is empty we remove it from our database
	if len(text) == 0 {
		// skip empty files from being added to db
		if post == nil {
			logger.Infof("(%s) is empty, skipping record", filename)
			return "", nil
		}

		err := h.DBPool.RemovePosts([]string{post.ID})
		logger.Infof("(%s) is empty, removing record", filename)
		if err != nil {
			logger.Errorf("error for %s: %v", filename, err)
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	} else if post == nil {
		logger.Infof("(%s) not found, adding record", filename)
		_, err = h.DBPool.InsertPost(
			userID,
			filename,
			metadata.Slug,
			metadata.Title,
			text,
			metadata.Description,
			metadata.PublishAt,
			metadata.Hidden,
			h.Cfg.Space,
		)
		if err != nil {
			logger.Errorf("error for %s: %v", filename, err)
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	} else {
		if text == post.Text {
			logger.Infof("(%s) found, but text is identical, skipping", filename)
			return h.Cfg.FullPostURL(user.Name, filename, h.Cfg.IsSubdomains(), true), nil
		}

		logger.Infof("(%s) found, updating record", filename)
		_, err = h.DBPool.UpdatePost(
			post.ID,
			metadata.Slug,
			metadata.Title,
			text,
			metadata.Description,
			metadata.PublishAt,
		)
		if err != nil {
			logger.Errorf("error for %s: %v", filename, err)
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	}

	return h.Cfg.FullPostURL(user.Name, filename, h.Cfg.IsSubdomains(), true), nil
}
