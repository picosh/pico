package pastes

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/araddon/dateparse"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
)

var DEFAULT_EXPIRES_AT = 90

// UploadHandler stores every file as a paste.
type UploadHandler struct {
	Cfg *Config
	DB  DB
}

var (
	_ sendutils.CopyFromClientHandler = (*UploadHandler)(nil)
	_ sendutils.WriteValidator        = (*UploadHandler)(nil)
	_ sendutils.FileRenamer           = (*UploadHandler)(nil)
)

func NewUploadHandler(cfg *Config, dbpool DB) *UploadHandler {
	return &UploadHandler{Cfg: cfg, DB: dbpool}
}

func (h *UploadHandler) GetLogger(s *pssh.SSHServerConnSession) *slog.Logger {
	logger := pssh.GetLogger(s)
	if logger == nil {
		if h.Cfg != nil && h.Cfg.Logger != nil {
			return h.Cfg.Logger
		}
		return slog.Default()
	}
	return logger
}

func (h *UploadHandler) Validate(s *pssh.SSHServerConnSession) error {
	user, err := filehandlers.SessionUser(s)
	if err != nil {
		return err
	}
	if _, err := db.FindAccess(h.DB, user.ID, "pastes"); err != nil {
		return err
	}
	h.GetLogger(s).Info("attempting to upload files", "user", user.Name, "space", h.Cfg.Space)
	return nil
}

func (h *UploadHandler) List(s *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	return filehandlers.BaseList(s, fpath, isDir, recursive, []string{h.Cfg.Space}, h.DB)
}

func (h *UploadHandler) Read(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) (os.FileInfo, sendutils.ReadAndReaderAtCloser, error) {
	return filehandlers.ReadPost(s, h.DB, h.Cfg.Space, entry)
}

func (h *UploadHandler) Delete(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) error {
	return filehandlers.DeletePost(s, h.DB, h.Cfg.Space, entry)
}

// ValidateWrite checks a paste's name before its contents arrive.
func (h *UploadHandler) ValidateWrite(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) error {
	if _, err := filehandlers.SessionUser(s); err != nil {
		return err
	}
	if entry.Mode.IsDir() {
		return fmt.Errorf("file entry is directory, but only files are supported: %s", entry.Filepath)
	}
	return h.validate(filehandlers.NewPost(filepath.Base(entry.Filepath), nil))
}

func (h *UploadHandler) Write(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) (string, error) {
	user, err := filehandlers.SessionUser(s)
	if err != nil {
		return "", err
	}
	logger := h.GetLogger(s).With("filename", filepath.Base(entry.Filepath))

	post, err := filehandlers.PostFromEntry(entry, user, h.Cfg.Space)
	if err != nil {
		return "", err
	}
	if err := h.validate(post); err != nil {
		logger.Error("file failed validation", "err", err.Error())
		return "", err
	}

	cur, err := filehandlers.LookupPost(h.DB, user.ID, h.Cfg.Space, post.Filename)
	if err != nil {
		return "", err
	}
	if cur != nil {
		post.Data, post.PublishAt = cur.Data, cur.PublishAt
	}
	setMeta(s, cur, post)

	_, changed, err := filehandlers.SavePost(h.DB, cur, post)
	if err != nil {
		logger.Error("paste could not be saved", "err", err.Error())
		return "", err
	}
	logger.Info("saved paste", "new", cur == nil, "changed", changed)
	return shared.NewCreateURL(h.Cfg.Protocol, h.Cfg.Domain).FullPostURL(user.Name, post.Slug), nil
}

// Rename moves a paste to a new filename in place, keeping its settings.
func (h *UploadHandler) Rename(s *pssh.SSHServerConnSession, from, to *sendutils.FileEntry) error {
	return filehandlers.RenamePost(s, h.DB, h.Cfg.Space, from, to, func(cur, next *db.Post) ([]string, error) {
		if err := h.validate(next); err != nil {
			return nil, err
		}
		setMeta(s, cur, next)
		return nil, nil
	})
}

func (h *UploadHandler) validate(post *db.Post) error {
	if !shared.IsTextFile(post.Text) {
		return fmt.Errorf(
			"ERROR: (%s) invalid file must be plain text (utf-8), skipping",
			post.Filename,
		)
	}

	maxFileSize := int(h.Cfg.MaxAssetSize)
	if post.FileSize > maxFileSize {
		return fmt.Errorf(
			"ERROR: file (%s) has exceeded maximum file size (%d bytes)",
			post.Filename,
			maxFileSize,
		)
	}

	return nil
}

// setMeta applies the paste's settings: those of cur, the paste it
// replaces, unless the upload command sets them.
func setMeta(s *pssh.SSHServerConnSession, cur, post *db.Post) {
	post.Title = shared.ToUpper(post.Slug)
	// we want the slug to be the filename for pastes
	post.Slug = post.Filename

	// mark posts for deletion a X days after creation
	expiresAt := time.Now().AddDate(0, 0, DEFAULT_EXPIRES_AT)
	post.ExpiresAt = &expiresAt
	var hidden bool
	// A paste written without arguments, as over sftp, keeps its settings.
	if cur != nil {
		hidden = cur.Hidden
		if cur.ExpiresAt == nil || cur.ExpiresAt.After(time.Now()) {
			post.ExpiresAt = cur.ExpiresAt
		}
	}

	var expiresFound bool
	var expires *time.Time

	cmd := s.Command()

	for _, arg := range cmd {
		if strings.Contains(arg, "=") {
			splitArg := strings.Split(arg, "=")
			if len(splitArg) != 2 {
				continue
			}

			switch splitArg[0] {
			case "hidden":
				val, err := strconv.ParseBool(splitArg[1])
				if err != nil {
					continue
				}

				hidden = val
			case "expires":
				val, err := strconv.ParseBool(splitArg[1])
				if err == nil {
					expiresFound = true
					expires = nil
					if val {
						expires = &expiresAt
					}
					continue
				}

				duration, err := time.ParseDuration(splitArg[1])
				if err == nil {
					expiresFound = true
					expireTime := time.Now().Add(duration)
					expires = &expireTime
					continue
				}

				expireTime, err := dateparse.ParseStrict(splitArg[1])
				if err == nil {
					expiresFound = true
					expires = &expireTime
				}
			}
		}
	}

	post.Hidden = hidden

	if expiresFound {
		post.ExpiresAt = expires
	}
}
