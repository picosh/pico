package feeds

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/adhocore/gronx"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
)

// UploadHandler stores every file as a feed.
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
	return pssh.GetLogger(s)
}

func (h *UploadHandler) Validate(s *pssh.SSHServerConnSession) error {
	user, err := filehandlers.SessionUser(s)
	if err != nil {
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

// ValidateWrite checks a feed's name before its contents arrive.
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
	// The feed's digest state lives in its data.
	if cur != nil {
		post.Data, post.PublishAt = cur.Data, cur.PublishAt
	}

	_, changed, err := filehandlers.SavePost(h.DB, cur, post)
	if err != nil {
		logger.Error("feed could not be saved", "err", err.Error())
		return "", err
	}
	logger.Info("saved feed", "new", cur == nil, "changed", changed)
	return shared.NewCreateURL(h.Cfg.Protocol, h.Cfg.Domain).FullPostURL(user.Name, post.Slug), nil
}

// Rename moves a feed to a new filename in place, keeping its digest state.
func (h *UploadHandler) Rename(s *pssh.SSHServerConnSession, from, to *sendutils.FileEntry) error {
	return filehandlers.RenamePost(s, h.DB, h.Cfg.Space, from, to, func(cur, next *db.Post) ([]string, error) {
		return nil, h.validate(next)
	})
}

func (h *UploadHandler) validate(post *db.Post) error {
	if !shared.IsTextFile(post.Text) {
		return fmt.Errorf(
			"WARNING: (%s) invalid file must be plain text (utf-8), skipping",
			post.Filename,
		)
	}

	if !shared.IsExtAllowed(post.Filename, h.Cfg.AllowedExt) {
		extStr := strings.Join(h.Cfg.AllowedExt, ",")
		return fmt.Errorf(
			"WARNING: (%s) invalid file, format must be (%s), skipping",
			post.Filename,
			extStr,
		)
	}

	// SFTP validates a file when it is opened, before its contents
	// arrive, so there is nothing to parse yet.
	if post.Text == "" {
		return nil
	}

	parsed := shared.ListParseText(post.Text)
	if parsed.Email == "" {
		return fmt.Errorf("ERROR: no email variable detected for %s, check the format of your file, skipping", post.Filename)
	}

	if parsed.DigestInterval != "" {
		return fmt.Errorf("ERROR: `digest_interval` is deprecated; use `cron`: https://pico.sh/feeds#cron")
	}

	if parsed.Cron != "" {
		if !gronx.IsValid(parsed.Cron) {
			return fmt.Errorf("ERROR: `cron` is invalid, reference: https://github.com/adhocore/gronx?tab=readme-ov-file#cron-expression")
		}
	}

	var allErr error
	for _, txt := range parsed.Items {
		u := ""
		if txt.IsText {
			u = txt.Value
		} else if txt.IsURL {
			u = string(txt.URL)
		}

		_, err := url.Parse(u)
		if err != nil {
			allErr = errors.Join(allErr, fmt.Errorf("%s: %w", u, err))
			continue
		}
	}
	if allErr != nil {
		return fmt.Errorf("ERROR: some urls provided were invalid check the format of your file, skipping: %w", allErr)
	}

	return nil
}
