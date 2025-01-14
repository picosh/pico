package filehandlers

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	sendutils "github.com/picosh/send/utils"
	"github.com/picosh/utils"
)

type PostMetaData struct {
	*db.Post
	Cur       *db.Post
	Tags      []string
	User      *db.User
	FileEntry *sendutils.FileEntry
	Aliases   []string
}

type SuccesHook struct {
	UserID   string `json:"user_id"`
	PostID   string `json:"post_id"`
	Action   string `json:"action"`
	Filename string `json:"filename"`
}

type ScpFileHooks interface {
	FileValidate(s ssh.Session, data *PostMetaData) (bool, error)
	FileMeta(s ssh.Session, data *PostMetaData) error
	FileSuccess(s ssh.Session, data *SuccesHook) error
}

type ScpUploadHandler struct {
	DBPool db.DB
	Cfg    *shared.ConfigSite
	Hooks  ScpFileHooks
}

func NewScpPostHandler(dbpool db.DB, cfg *shared.ConfigSite, hooks ScpFileHooks, st storage.StorageServe) *ScpUploadHandler {
	return &ScpUploadHandler{
		DBPool: dbpool,
		Cfg:    cfg,
		Hooks:  hooks,
	}
}

func (h *ScpUploadHandler) Read(s ssh.Session, entry *sendutils.FileEntry) (os.FileInfo, sendutils.ReaderAtCloser, error) {
	user, err := h.DBPool.FindUser(s.Permissions().Extensions["user_id"])
	if err != nil {
		return nil, nil, err
	}
	cleanFilename := filepath.Base(entry.Filepath)

	if cleanFilename == "" || cleanFilename == "." {
		return nil, nil, os.ErrNotExist
	}

	post, err := h.DBPool.FindPostWithFilename(cleanFilename, user.ID, h.Cfg.Space)
	if err != nil {
		return nil, nil, err
	}

	fileInfo := &sendutils.VirtualFile{
		FName:    post.Filename,
		FIsDir:   false,
		FSize:    int64(post.FileSize),
		FModTime: *post.UpdatedAt,
	}

	reader := sendutils.NopReaderAtCloser(strings.NewReader(post.Text))

	return fileInfo, reader, nil
}

func (h *ScpUploadHandler) Write(s ssh.Session, entry *sendutils.FileEntry) (string, error) {
	logger := h.Cfg.Logger
	user, err := h.DBPool.FindUser(s.Permissions().Extensions["user_id"])
	if err != nil {
		logger.Error("error getting user from ctx", "err", err.Error())
		return "", err
	}

	userID := user.ID
	filename := filepath.Base(entry.Filepath)
	logger = shared.LoggerWithUser(logger, user)
	logger = logger.With(
		"filename", filename,
	)

	if entry.Mode.IsDir() {
		return "", fmt.Errorf("file entry is directory, but only files are supported: %s", filename)
	}

	var origText []byte
	if b, err := io.ReadAll(entry.Reader); err == nil {
		origText = b
	}

	mimeType := http.DetectContentType(origText)
	ext := filepath.Ext(filename)
	// DetectContentType does not detect markdown
	if ext == ".md" {
		mimeType = "text/markdown; charset=UTF-8"
	}

	now := time.Now()
	slug := utils.SanitizeFileExt(filename)
	fileSize := binary.Size(origText)
	shasum := utils.Shasum(origText)

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
		User:      user,
		FileEntry: entry,
	}

	valid, err := h.Hooks.FileValidate(s, &metadata)
	if !valid {
		logger.Error("file failed validation", "err", err.Error())
		return "", err
	}

	post, err := h.DBPool.FindPostWithFilename(metadata.Filename, metadata.User.ID, h.Cfg.Space)
	if err != nil {
		logger.Error("unable to load post, continuing", "err", err.Error())
	}

	if post != nil {
		metadata.Cur = post
		metadata.Data = post.Data
		metadata.Post.PublishAt = post.PublishAt
	}

	err = h.Hooks.FileMeta(s, &metadata)
	if err != nil {
		logger.Error("file could not load meta", "err", err.Error())
		return "", err
	}

	modTime := time.Now()

	if entry.Mtime > 0 {
		modTime = time.Unix(entry.Mtime, 0)
	}

	action := ""
	if post == nil {
		action = "create"
		logger.Info("file not found, adding record")
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
			ExpiresAt:   metadata.ExpiresAt,
			UpdatedAt:   &modTime,
		}
		post, err = h.DBPool.InsertPost(&insertPost)
		if err != nil {
			logger.Error("post could not be created", "err", err.Error())
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}

		if len(metadata.Aliases) > 0 {
			logger.Info(
				"found post aliases, replacing with old aliases",
				"aliases",
				strings.Join(metadata.Aliases, ","),
			)
			err = h.DBPool.ReplaceAliasesForPost(metadata.Aliases, post.ID)
			if err != nil {
				logger.Error("post could not replace aliases", "err", err.Error())
				return "", fmt.Errorf("error for %s: %v", filename, err)
			}
		}

		if len(metadata.Tags) > 0 {
			logger.Info(
				"found post tags, replacing with old tags",
				"tags", strings.Join(metadata.Tags, ","),
			)
			err = h.DBPool.ReplaceTagsForPost(metadata.Tags, post.ID)
			if err != nil {
				logger.Error("post could not replace tags", "err", err.Error())
				return "", fmt.Errorf("error for %s: %v", filename, err)
			}
		}
	} else {
		if metadata.Text == post.Text && modTime.Equal(*post.UpdatedAt) {
			logger.Info("file found, but text is identical, skipping")
			curl := shared.NewCreateURL(h.Cfg)
			return h.Cfg.FullPostURL(curl, user.Name, metadata.Slug), nil
		}

		action = "update"
		logger.Info("file found, updating record")

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
			Hidden:      metadata.Hidden,
			ExpiresAt:   metadata.ExpiresAt,
			UpdatedAt:   &modTime,
		}
		_, err = h.DBPool.UpdatePost(&updatePost)
		if err != nil {
			logger.Error("post could not be updated", "err", err.Error())
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}

		logger.Info(
			"found post tags, replacing with old tags",
			"tags", strings.Join(metadata.Tags, ","),
		)
		err = h.DBPool.ReplaceTagsForPost(metadata.Tags, post.ID)
		if err != nil {
			logger.Error("post could not replace tags", "err", err.Error())
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}

		logger.Info(
			"found post aliases, replacing with old aliases",
			"aliases", strings.Join(metadata.Aliases, ","),
		)
		err = h.DBPool.ReplaceAliasesForPost(metadata.Aliases, post.ID)
		if err != nil {
			logger.Error("post could not replace aliases", "err", err.Error())
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	}

	_ = h.Hooks.FileSuccess(s, &SuccesHook{
		UserID:   user.ID,
		PostID:   post.ID,
		Action:   action,
		Filename: metadata.Filename,
	})
	curl := shared.NewCreateURL(h.Cfg)
	return h.Cfg.FullPostURL(curl, user.Name, metadata.Slug), nil
}

func (h *ScpUploadHandler) Delete(s ssh.Session, entry *sendutils.FileEntry) error {
	logger := h.Cfg.Logger
	user, err := h.DBPool.FindUser(s.Permissions().Extensions["user_id"])
	if err != nil {
		logger.Error("could not get user from ctx", "err", err.Error())
		return err
	}

	userID := user.ID
	filename := filepath.Base(entry.Filepath)
	logger = shared.LoggerWithUser(logger, user)
	logger = logger.With(
		"filename", filename,
	)

	post, err := h.DBPool.FindPostWithFilename(filename, userID, h.Cfg.Space)
	if err != nil {
		return err
	}

	if post == nil {
		return os.ErrNotExist
	}

	err = h.DBPool.RemovePosts([]string{post.ID})
	logger.Info("removing record")
	if err != nil {
		logger.Error("post could not remove", "err", err.Error())
		return fmt.Errorf("error for %s: %v", filename, err)
	}
	_ = h.Hooks.FileSuccess(s, &SuccesHook{
		UserID:   user.ID,
		PostID:   post.ID,
		Action:   "delete",
		Filename: filename,
	})
	return nil
}
