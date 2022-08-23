package filehandlers

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/imgs"
	"git.sr.ht/~erock/pico/shared"
	"git.sr.ht/~erock/pico/wish/cms/util"
	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/gliderlabs/ssh"
)

type PostMetaData struct {
	*db.Post
	Cur       *db.Post
	Tags      []string
	User      *db.User
	FileEntry *utils.FileEntry
}

type ScpFileHooks interface {
	FileValidate(data *PostMetaData) (bool, error)
	FileMeta(data *PostMetaData) error
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

	client := imgs.NewImgsAPI(h.DBPool)
	if shared.IsExtAllowed(filename, client.Cfg.AllowedExt) {
		if !client.HasAccess(userID) {
			msg := "user (%s) does not have access to imgs.sh, cannot upload file (%s)"
			return "", fmt.Errorf(msg, h.User.Name, filename)
		}

		return client.Upload(s, entry)
	}

	var origText []byte
	if b, err := io.ReadAll(entry.Reader); err == nil {
		origText = b
	}

	mimeType := http.DetectContentType(origText)
	ext := path.Ext(filename)
	// DetectContentType does not detect markdown
	if ext == ".md" {
		mimeType = "text/markdown; charset=UTF-8"
	}

	now := time.Now()
	slug := shared.SanitizeFileExt(filename)
	fileSize := binary.Size(origText)
	shasum := shared.Shasum(origText)

	nextPost := db.Post{
		Filename:  filename,
		Slug:      slug,
		PublishAt: &now,
		Text:      string(origText),
		MimeType:  mimeType,
		FileSize:  fileSize,
		Shasum:    shasum,
	}

	metadata := PostMetaData{
		Post:      &nextPost,
		User:      h.User,
		FileEntry: entry,
	}

	valid, err := h.Hooks.FileValidate(&metadata)
	if !valid {
		logger.Info(err)
		return "", err
	}

	post, err := h.DBPool.FindPostWithFilename(metadata.Filename, metadata.User.ID, h.Cfg.Space)
	if err != nil {
		logger.Infof("unable to load post (%s), continuing", filename)
		logger.Info(err)
	}

	if post != nil {
		metadata.Cur = post
		metadata.Post.PublishAt = post.PublishAt
	}

	err = h.Hooks.FileMeta(&metadata)
	if err != nil {
		logger.Error(err)
		return "", err
	}

	// if the file is empty we remove it from our database
	if len(origText) == 0 {
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
		insertPost := db.Post{
			UserID: userID,
			Space:  h.Cfg.Space,

			Data:        metadata.Data,
			Description: metadata.Description,
			Filename:    metadata.Filename,
			FileSize:    metadata.FileSize,
			Hidden:      metadata.Hidden,
			MimeType:    metadata.MimeType,
			PublishAt:   metadata.PublishAt,
			Shasum:      metadata.Shasum,
			Slug:        metadata.Slug,
			Text:        metadata.Text,
			Title:       metadata.Title,
		}
		post, err = h.DBPool.InsertPost(&insertPost)
		if err != nil {
			logger.Errorf("error for %s: %v", filename, err)
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}

		if len(metadata.Tags) > 0 {
			logger.Infof(
				"Found (%s) post tags, replacing with old tags",
				strings.Join(metadata.Tags, ","),
			)
			err = h.DBPool.ReplaceTagsForPost(metadata.Tags, post.ID)
			if err != nil {
				logger.Errorf("error for %s: %v", filename, err)
				return "", fmt.Errorf("error for %s: %v", filename, err)
			}
		}
	} else {
		if metadata.Text == post.Text {
			logger.Infof("(%s) found, but text is identical, skipping", filename)
			return h.Cfg.FullPostURL(h.User.Name, metadata.Slug, h.Cfg.IsSubdomains(), true), nil
		}

		logger.Infof("(%s) found, updating record", filename)
		updatePost := db.Post{
			ID: post.ID,

			Data:        metadata.Data,
			FileSize:    metadata.FileSize,
			Description: metadata.Description,
			PublishAt:   metadata.PublishAt,
			Slug:        metadata.Slug,
			Shasum:      metadata.Shasum,
			Text:        metadata.Text,
			Title:       metadata.Title,
		}
		_, err = h.DBPool.UpdatePost(&updatePost)
		if err != nil {
			logger.Errorf("error for %s: %v", filename, err)
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}

		logger.Infof(
			"Found (%s) post tags, replacing with old tags",
			strings.Join(metadata.Tags, ","),
		)
		err = h.DBPool.ReplaceTagsForPost(metadata.Tags, post.ID)
		if err != nil {
			logger.Errorf("error for %s: %v", filename, err)
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	}

	return h.Cfg.FullPostURL(h.User.Name, metadata.Slug, h.Cfg.IsSubdomains(), true), nil
}
