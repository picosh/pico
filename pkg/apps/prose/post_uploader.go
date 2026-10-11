package prose

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/postfs"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
)

// postUploader stores prose's text files as posts.
type postUploader struct {
	Cfg *Config
	DB  DB
}

var (
	_ ReadWriteHandler         = (*postUploader)(nil)
	_ sendutils.WriteValidator = (*postUploader)(nil)
	_ sendutils.FileRenamer    = (*postUploader)(nil)
)

func (h *postUploader) List(s *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	return postfs.ListPosts(s, fpath, isDir, recursive, []string{h.Cfg.Space}, h.DB)
}

func (h *postUploader) Read(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) (os.FileInfo, sendutils.ReadAndReaderAtCloser, error) {
	return postfs.ReadPost(s, h.DB, h.Cfg.Space, entry)
}

func (h *postUploader) Delete(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) error {
	return postfs.DeletePost(s, h.DB, h.Cfg.Space, entry)
}

// ValidateWrite checks a post's name before its contents arrive.
func (h *postUploader) ValidateWrite(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) error {
	if _, err := postfs.SessionUser(s); err != nil {
		return err
	}
	if entry.Mode.IsDir() {
		return fmt.Errorf("file entry is directory, but only files are supported: %s", entry.Filepath)
	}
	return h.validate(postfs.NewPost(filepath.Base(entry.Filepath), nil))
}

func (h *postUploader) Write(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) (string, error) {
	user, err := postfs.SessionUser(s)
	if err != nil {
		return "", err
	}
	logger := pssh.GetLogger(s).With("filename", filepath.Base(entry.Filepath))

	post, err := postfs.PostFromEntry(entry, user, h.Cfg.Space)
	if err != nil {
		return "", err
	}
	if err := h.validate(post); err != nil {
		logger.Error("file failed validation", "err", err.Error())
		return "", err
	}

	cur, err := postfs.LookupPost(h.DB, user.ID, h.Cfg.Space, post.Filename)
	if err != nil {
		return "", err
	}
	if cur != nil {
		post.Data, post.PublishAt = cur.Data, cur.PublishAt
	}
	aliases, err := h.setMeta(post)
	if err != nil {
		logger.Error("file could not load meta", "err", err.Error())
		return "", err
	}

	saved, changed, err := postfs.SavePost(h.DB, cur, post)
	if err != nil {
		logger.Error("post could not be saved", "err", err.Error())
		return "", err
	}
	logger.Info("saved post", "new", cur == nil, "changed", changed)
	// A new post has no tags or aliases to clear.
	if changed && (cur != nil || len(post.Tags) > 0) {
		if err := h.DB.ReplaceTagsByPost(post.Tags, saved.ID); err != nil {
			logger.Error("post could not replace tags", "err", err.Error())
			return "", fmt.Errorf("error for %s: %v", post.Filename, err)
		}
	}
	if changed && (cur != nil || len(aliases) > 0) {
		if err := h.DB.ReplaceAliasesByPost(aliases, saved.ID); err != nil {
			logger.Error("post could not replace aliases", "err", err.Error())
			return "", fmt.Errorf("error for %s: %v", post.Filename, err)
		}
	}

	return shared.NewCreateURL(h.Cfg.Protocol, h.Cfg.Domain).FullPostURL(user.Name, post.Slug), nil
}

// Rename moves a post to a new filename in place, so it keeps its ID,
// publish date and settings. A post at the new filename is replaced.
func (h *postUploader) Rename(s *pssh.SSHServerConnSession, from, to *sendutils.FileEntry) error {
	return postfs.RenamePost(s, h.DB, h.Cfg.Space, from, to, func(cur, next *db.Post) ([]string, error) {
		if err := h.validate(next); err != nil {
			return nil, err
		}
		return h.setMeta(next)
	})
}

func (h *postUploader) validate(post *db.Post) error {
	if !shared.IsTextFile(post.Text) {
		return fmt.Errorf(
			"ERROR: (%s) invalid file must be plain text (utf-8), skipping",
			post.Filename,
		)
	}

	fp := strings.Replace(post.Filename, "/", "", 1)
	// special styles css file we want to permit but no other css file.
	// sometimes the directory is provided in the filename, so we want to
	// remove that before we perform this check.
	if fp == "_styles.css" {
		return nil
	}
	// allow users to upload robots file
	if fp == "robots.txt" {
		return nil
	}

	if !shared.IsExtAllowed(post.Filename, h.Cfg.AllowedExt) {
		extStr := strings.Join(h.Cfg.AllowedExt, ",")
		return fmt.Errorf(
			"ERROR: (%s) invalid file, format must be (%s), skipping",
			post.Filename,
			extStr,
		)
	}

	if post.FileSize > MAX_FILE_SIZE {
		return fmt.Errorf(
			"ERROR: file (%s) has exceeded maximum file size (%d bytes)",
			post.Filename,
			MAX_FILE_SIZE,
		)
	}

	return nil
}

// setMeta fills in a post's title, description, tags, publish date and
// hidden flag from its text, and returns its aliases.
func (h *postUploader) setMeta(post *db.Post) ([]string, error) {
	var aliases []string
	switch filepath.Ext(post.Filename) {
	case ".lxt":
		parsedText := shared.ListParseText(post.Text)
		if parsedText.Title == "" {
			post.Title = shared.ToUpper(post.Slug)
		} else {
			post.Title = parsedText.Title
		}
		aliases = parsedText.Aliases
		post.Tags = parsedText.Tags
		post.Description = parsedText.Description
		if parsedText.PublishAt != nil && !parsedText.PublishAt.IsZero() {
			post.PublishAt = parsedText.PublishAt
		}
		post.Hidden = parsedText.Hidden
	case ".md":
		parsedText, err := shared.ParseText(post.Text)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", post.Filename, err)
		}
		if parsedText.Title == "" {
			post.Title = shared.ToUpper(post.Slug)
		} else {
			post.Title = parsedText.Title
		}
		aliases = parsedText.Aliases
		post.Tags = parsedText.Tags
		post.Description = parsedText.Description
		if parsedText.PublishAt != nil && !parsedText.PublishAt.IsZero() {
			post.PublishAt = parsedText.PublishAt
		}
		post.Hidden = parsedText.Hidden
	}

	post.Hidden = post.Hidden || slices.Contains(h.Cfg.HiddenPosts, post.Filename)
	return aliases, nil
}
