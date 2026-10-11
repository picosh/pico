package filehandlers

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
)

type PostMetaData struct {
	*db.Post
	Cur       *db.Post
	Tags      []string
	User      *db.User
	FileEntry *sendutils.FileEntry
	Aliases   []string
}

type ScpFileHooks interface {
	FileValidate(s *pssh.SSHServerConnSession, data *PostMetaData) (bool, error)
	FileMeta(s *pssh.SSHServerConnSession, data *PostMetaData) error
}

type ScpUploadHandler struct {
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

func (r *ScpUploadHandler) List(s *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	return BaseList(s, fpath, isDir, recursive, []string{r.Cfg.Space}, r.DBPool)
}

func (h *ScpUploadHandler) Read(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) (os.FileInfo, sendutils.ReadAndReaderAtCloser, error) {
	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
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

	reader := sendutils.NopReadAndReaderAtCloser(strings.NewReader(post.Text))

	return fileInfo, reader, nil
}

// ValidateWrite runs the hooks' validation on the file before its
// contents arrive.
func (h *ScpUploadHandler) ValidateWrite(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) error {
	user := pssh.GetUser(s)
	if user == nil {
		return fmt.Errorf("could not get user from ctx")
	}
	if entry.Mode.IsDir() {
		return fmt.Errorf("file entry is directory, but only files are supported: %s", entry.Filepath)
	}
	filename := filepath.Base(entry.Filepath)
	metadata := PostMetaData{
		Post:      &db.Post{Filename: filename, Slug: shared.SanitizeFileExt(filename)},
		User:      user,
		FileEntry: entry,
	}
	if valid, err := h.Hooks.FileValidate(s, &metadata); !valid {
		return err
	}
	return nil
}

func (h *ScpUploadHandler) Write(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) (string, error) {
	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return "", err
	}

	userID := user.ID
	filename := filepath.Base(entry.Filepath)

	logger = logger.With(
		"filename", filename,
	)

	if entry.Mode.IsDir() {
		return "", fmt.Errorf("file entry is directory, but only files are supported: %s", filename)
	}

	origText, err := io.ReadAll(entry.Reader)
	if err != nil {
		return "", fmt.Errorf("could not read file (%s): %w", filename, err)
	}

	nextPost := newPost(filename, origText)
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
		metadata.PublishAt = post.PublishAt
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

	if post == nil {
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
			err = h.DBPool.ReplaceAliasesByPost(metadata.Aliases, post.ID)
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
			err = h.DBPool.ReplaceTagsByPost(metadata.Tags, post.ID)
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
		err = h.DBPool.ReplaceTagsByPost(metadata.Tags, post.ID)
		if err != nil {
			logger.Error("post could not replace tags", "err", err.Error())
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}

		logger.Info(
			"found post aliases, replacing with old aliases",
			"aliases", strings.Join(metadata.Aliases, ","),
		)
		err = h.DBPool.ReplaceAliasesByPost(metadata.Aliases, post.ID)
		if err != nil {
			logger.Error("post could not replace aliases", "err", err.Error())
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	}

	curl := shared.NewCreateURL(h.Cfg)
	return h.Cfg.FullPostURL(curl, user.Name, metadata.Slug), nil
}

// newPost is the post a file makes before the hooks fill in the rest.
func newPost(filename string, text []byte) db.Post {
	mimeType := http.DetectContentType(text)
	// DetectContentType does not detect markdown
	if filepath.Ext(filename) == ".md" {
		mimeType = "text/markdown; charset=UTF-8"
	}
	now := time.Now()
	return db.Post{
		Filename:  filename,
		Slug:      shared.SanitizeFileExt(filename),
		PublishAt: &now,
		Text:      string(text),
		MimeType:  mimeType,
		FileSize:  binary.Size(text),
		Shasum:    shared.Shasum(text),
	}
}

// Rename moves a post to a new filename in place, so it keeps its ID,
// publish date and settings. A post at the new filename is replaced.
func (h *ScpUploadHandler) Rename(s *pssh.SSHServerConnSession, from, to *sendutils.FileEntry) error {
	user := pssh.GetUser(s)
	if user == nil {
		return fmt.Errorf("could not get user from ctx")
	}
	fromName, toName := filepath.Base(from.Filepath), filepath.Base(to.Filepath)
	// A save to either post while it is being renamed starts it over.
	var err error
	for range 3 {
		if err = h.rename(s, user, fromName, toName, to); !errors.Is(err, db.ErrPostChanged) {
			break
		}
	}
	if err != nil {
		return err
	}
	pssh.GetLogger(s).Info("renamed post", "from", fromName, "to", toName)
	return nil
}

func (h *ScpUploadHandler) rename(s *pssh.SSHServerConnSession, user *db.User, fromName, toName string, to *sendutils.FileEntry) error {
	post, err := h.DBPool.FindPostWithFilename(fromName, user.ID, h.Cfg.Space)
	if errors.Is(err, sql.ErrNoRows) {
		return os.ErrNotExist
	}
	if err != nil {
		return err
	}
	if fromName == toName {
		return nil
	}
	replaced, err := h.DBPool.FindPostWithFilename(toName, user.ID, h.Cfg.Space)
	if errors.Is(err, sql.ErrNoRows) {
		replaced = nil
	} else if err != nil {
		return err
	}

	next := newPost(toName, []byte(post.Text))
	metadata := PostMetaData{
		Post:      &next,
		Cur:       post,
		User:      user,
		FileEntry: to,
	}
	if valid, err := h.Hooks.FileValidate(s, &metadata); !valid {
		return err
	}
	metadata.Data = post.Data
	metadata.PublishAt = post.PublishAt
	if err := h.Hooks.FileMeta(s, &metadata); err != nil {
		return err
	}
	if other, err := h.DBPool.FindPostWithSlug(metadata.Slug, user.ID, h.Cfg.Space); err == nil &&
		other.ID != post.ID && (replaced == nil || other.ID != replaced.ID) {
		return fmt.Errorf("%s: %s already uses its slug (%s): %w", toName, other.Filename, metadata.Slug, os.ErrExist)
	}

	metadata.UpdatedAt = post.UpdatedAt
	// The new name can parse the text differently, as .md and .lxt do.
	_, err = h.DBPool.RenamePost(post, metadata.Post, replaced, metadata.Tags, metadata.Aliases)
	if err != nil && !errors.Is(err, db.ErrPostChanged) {
		return fmt.Errorf("error for %s: %w", toName, err)
	}
	return err
}

func (h *ScpUploadHandler) Delete(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) error {
	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return err
	}

	userID := user.ID
	filename := filepath.Base(entry.Filepath)
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
	return nil
}
