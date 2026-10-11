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

// SessionUser is the user an upload session belongs to.
func SessionUser(s *pssh.SSHServerConnSession) (*db.User, error) {
	user := pssh.GetUser(s)
	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		pssh.GetLogger(s).Error("error getting user from ctx", "err", err)
		return nil, err
	}
	return user, nil
}

// NewPost is the post a file makes before an app fills in the rest.
func NewPost(filename string, text []byte) *db.Post {
	mimeType := http.DetectContentType(text)
	// DetectContentType does not detect markdown
	if filepath.Ext(filename) == ".md" {
		mimeType = "text/markdown; charset=UTF-8"
	}
	now := time.Now()
	return &db.Post{
		Filename:  filename,
		Slug:      shared.SanitizeFileExt(filename),
		PublishAt: &now,
		UpdatedAt: &now,
		Text:      string(text),
		MimeType:  mimeType,
		FileSize:  binary.Size(text),
		Shasum:    shared.Shasum(text),
	}
}

// PostFromEntry reads an uploaded file into a new post for user in space.
func PostFromEntry(entry *sendutils.FileEntry, user *db.User, space string) (*db.Post, error) {
	if entry.Mode.IsDir() {
		return nil, os.ErrInvalid
	}
	filename := filepath.Base(entry.Filepath)
	text, err := io.ReadAll(entry.Reader)
	if err != nil {
		return nil, fmt.Errorf("could not read file (%s): %w", filename, err)
	}
	post := NewPost(filename, text)
	post.UserID = user.ID
	post.Space = space
	if entry.Mtime > 0 {
		modTime := time.Unix(entry.Mtime, 0)
		post.UpdatedAt = &modTime
	}
	return post, nil
}

// LookupPost finds a user's post by filename, or nil when there is none.
func LookupPost(dbpool db.PostDB, userID, space, filename string) (*db.Post, error) {
	post, err := dbpool.FindPostWithFilename(filename, userID, space)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return post, err
}

// SavePost inserts next, or updates cur with it. It reports false without
// writing when cur already has next's text and mod time.
func SavePost(dbpool db.PostDB, cur, next *db.Post) (*db.Post, bool, error) {
	if cur == nil {
		post, err := dbpool.InsertPost(&db.Post{
			UserID:      next.UserID,
			Space:       next.Space,
			Data:        next.Data,
			Description: next.Description,
			Filename:    next.Filename,
			FileSize:    next.FileSize,
			Hidden:      next.Hidden,
			MimeType:    next.MimeType,
			PublishAt:   next.PublishAt,
			Shasum:      next.Shasum,
			Slug:        next.Slug,
			Text:        next.Text,
			Title:       next.Title,
			ExpiresAt:   next.ExpiresAt,
			UpdatedAt:   next.UpdatedAt,
		})
		if err != nil {
			return nil, false, fmt.Errorf("error for %s: %v", next.Filename, err)
		}
		return post, true, nil
	}

	if next.Text == cur.Text && next.UpdatedAt.Equal(*cur.UpdatedAt) {
		return cur, false, nil
	}
	post, err := dbpool.UpdatePost(&db.Post{
		ID:          cur.ID,
		Data:        next.Data,
		FileSize:    next.FileSize,
		Description: next.Description,
		PublishAt:   next.PublishAt,
		Slug:        next.Slug,
		Shasum:      next.Shasum,
		Text:        next.Text,
		Title:       next.Title,
		Hidden:      next.Hidden,
		ExpiresAt:   next.ExpiresAt,
		UpdatedAt:   next.UpdatedAt,
	})
	if err != nil {
		return nil, false, fmt.Errorf("error for %s: %v", next.Filename, err)
	}
	return post, true, nil
}

// ReadPost returns a user's post as a file.
func ReadPost(s *pssh.SSHServerConnSession, dbpool db.PostDB, space string, entry *sendutils.FileEntry) (os.FileInfo, sendutils.ReadAndReaderAtCloser, error) {
	user, err := SessionUser(s)
	if err != nil {
		return nil, nil, err
	}

	cleanFilename := filepath.Base(entry.Filepath)
	if cleanFilename == "" || cleanFilename == "." {
		return nil, nil, os.ErrNotExist
	}

	post, err := dbpool.FindPostWithFilename(cleanFilename, user.ID, space)
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

// DeletePost removes a user's post.
func DeletePost(s *pssh.SSHServerConnSession, dbpool db.PostDB, space string, entry *sendutils.FileEntry) error {
	user, err := SessionUser(s)
	if err != nil {
		return err
	}

	filename := filepath.Base(entry.Filepath)
	logger := pssh.GetLogger(s).With("filename", filename)

	post, err := dbpool.FindPostWithFilename(filename, user.ID, space)
	if err != nil {
		return err
	}
	if post == nil {
		return os.ErrNotExist
	}

	logger.Info("removing record")
	if err := dbpool.RemovePosts([]string{post.ID}); err != nil {
		logger.Error("post could not remove", "err", err.Error())
		return fmt.Errorf("error for %s: %v", filename, err)
	}
	return nil
}

// RenameFunc makes the post that cur becomes under the filename next
// already has, with next.Tags and the returned aliases.
type RenameFunc func(cur, next *db.Post) (aliases []string, err error)

// RenamePost moves a user's post to a new filename in place, so it keeps its
// ID, publish date and settings. A post at the new filename is replaced.
func RenamePost(s *pssh.SSHServerConnSession, dbpool db.PostDB, space string, from, to *sendutils.FileEntry, rename RenameFunc) error {
	user, err := SessionUser(s)
	if err != nil {
		return err
	}
	fromName, toName := filepath.Base(from.Filepath), filepath.Base(to.Filepath)
	// A save to either post while it is being renamed starts it over.
	for range 3 {
		if err = renamePost(dbpool, user, space, fromName, toName, rename); !errors.Is(err, db.ErrPostChanged) {
			break
		}
	}
	if err != nil {
		return err
	}
	pssh.GetLogger(s).Info("renamed post", "from", fromName, "to", toName)
	return nil
}

func renamePost(dbpool db.PostDB, user *db.User, space, fromName, toName string, rename RenameFunc) error {
	post, err := dbpool.FindPostWithFilename(fromName, user.ID, space)
	if errors.Is(err, sql.ErrNoRows) {
		return os.ErrNotExist
	}
	if err != nil {
		return err
	}
	if fromName == toName {
		return nil
	}
	replaced, err := LookupPost(dbpool, user.ID, space, toName)
	if err != nil {
		return err
	}

	next := NewPost(toName, []byte(post.Text))
	next.UserID, next.Space = post.UserID, post.Space
	next.Data, next.PublishAt = post.Data, post.PublishAt
	aliases, err := rename(post, next)
	if err != nil {
		return err
	}
	if other, err := dbpool.FindPostWithSlug(next.Slug, user.ID, space); err == nil &&
		other.ID != post.ID && (replaced == nil || other.ID != replaced.ID) {
		return fmt.Errorf("%s: %s already uses its slug (%s): %w", toName, other.Filename, next.Slug, os.ErrExist)
	}

	next.UpdatedAt = post.UpdatedAt
	// The new name can parse the text differently, as .md and .lxt do.
	_, err = dbpool.RenamePost(post, next, replaced, next.Tags, aliases)
	if err != nil && !errors.Is(err, db.ErrPostChanged) {
		return fmt.Errorf("error for %s: %w", toName, err)
	}
	return err
}
