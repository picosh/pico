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
	"github.com/picosh/pico/filehandlers/util"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/send/send/utils"
)

type PostMetaData struct {
	*db.Post
	Cur       *db.Post
	Tags      []string
	User      *db.User
	FileEntry *utils.FileEntry
	Aliases   []string
}

type ScpFileHooks interface {
	FileValidate(s ssh.Session, data *PostMetaData) (bool, error)
	FileMeta(s ssh.Session, data *PostMetaData) error
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

func (h *ScpUploadHandler) Read(s ssh.Session, entry *utils.FileEntry) (os.FileInfo, utils.ReaderAtCloser, error) {
	user, err := util.GetUser(s)
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

	fileInfo := &utils.VirtualFile{
		FName:    post.Filename,
		FIsDir:   false,
		FSize:    int64(post.FileSize),
		FModTime: *post.UpdatedAt,
	}

	reader := utils.NopReaderAtCloser(strings.NewReader(post.Text))

	return fileInfo, reader, nil
}

func (h *ScpUploadHandler) Write(s ssh.Session, entry *utils.FileEntry) (string, error) {
	logger := h.Cfg.Logger
	user, err := util.GetUser(s)
	if err != nil {
		logger.Error(err.Error())
		return "", err
	}

	userID := user.ID
	filename := filepath.Base(entry.Filepath)
	logger = logger.With(
		"user", user.Name,
		"filename", filename,
	)

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
		User:      user,
		FileEntry: entry,
	}

	valid, err := h.Hooks.FileValidate(s, &metadata)
	if !valid {
		logger.Error(err.Error())
		return "", err
	}

	post, err := h.DBPool.FindPostWithFilename(metadata.Filename, metadata.User.ID, h.Cfg.Space)
	if err != nil {
		logger.Info("unable to load post, continuing", "err", err.Error())
	}

	if post != nil {
		metadata.Cur = post
		metadata.Post.PublishAt = post.PublishAt
	}

	err = h.Hooks.FileMeta(s, &metadata)
	if err != nil {
		logger.Error(err.Error())
		return "", err
	}

	modTime := time.Unix(entry.Mtime, 0)

	// if the file is empty we remove it from our database
	if len(origText) == 0 {
		// skip empty files from being added to db
		if post == nil {
			logger.Info("file is empty, skipping record")
			return "", nil
		}

		err := h.DBPool.RemovePosts([]string{post.ID})
		logger.Info("file is empty, removing record")
		if err != nil {
			logger.Error(err.Error())
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	} else if post == nil {
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
			logger.Error(err.Error())
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
				logger.Error(err.Error())
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
				logger.Error(err.Error())
				return "", fmt.Errorf("error for %s: %v", filename, err)
			}
		}
	} else {
		if metadata.Text == post.Text && modTime.Equal(*post.UpdatedAt) {
			logger.Info("file found, but text is identical, skipping")
			curl := shared.NewCreateURL(h.Cfg)
			return h.Cfg.FullPostURL(curl, user.Name, metadata.Slug), nil
		}

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
			logger.Error(err.Error())
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}

		logger.Info(
			"found post tags, replacing with old tags",
			"tags", strings.Join(metadata.Tags, ","),
		)
		err = h.DBPool.ReplaceTagsForPost(metadata.Tags, post.ID)
		if err != nil {
			logger.Error(err.Error())
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}

		logger.Info(
			"found post aliases, replacing with old aliases",
			"aliases", strings.Join(metadata.Aliases, ","),
		)
		err = h.DBPool.ReplaceAliasesForPost(metadata.Aliases, post.ID)
		if err != nil {
			logger.Error(err.Error())
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	}

	curl := shared.NewCreateURL(h.Cfg)
	return h.Cfg.FullPostURL(curl, user.Name, metadata.Slug), nil
}
