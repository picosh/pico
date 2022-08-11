package upload

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"path"
	"time"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/imgs"
	"git.sr.ht/~erock/pico/shared"
	"git.sr.ht/~erock/pico/wish/cms/util"
	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/gliderlabs/ssh"
)

var GB = 1024 * 1024 * 1024
var maxSize = 2 * GB
var mdMime = "text/markdown; charset=UTF-8"

type UploadImgHandler struct {
	User    *db.User
	DBPool  db.DB
	Cfg     *shared.ConfigSite
	Storage *imgs.StorageFS
}

func NewUploadImgHandler(dbpool db.DB, cfg *shared.ConfigSite, storage *imgs.StorageFS) *UploadImgHandler {
	return &UploadImgHandler{
		DBPool:  dbpool,
		Cfg:     cfg,
		Storage: storage,
	}
}

func (h *UploadImgHandler) removePost(data *filehandlers.PostMetaData) error {
	// skip empty files from being added to db
	if data.Post == nil {
		h.Cfg.Logger.Infof("(%s) is empty, skipping record", data.Filename)
		return nil
	}

	err := h.DBPool.RemovePosts([]string{data.Post.ID})
	h.Cfg.Logger.Infof("(%s) is empty, removing record", data.Filename)
	if err != nil {
		h.Cfg.Logger.Errorf("error for %s: %v", data.Filename, err)
		return fmt.Errorf("error for %s: %v", data.Filename, err)
	}

	return nil
}

func (h *UploadImgHandler) Validate(s ssh.Session) error {
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

func (h *UploadImgHandler) Write(s ssh.Session, entry *utils.FileEntry) (string, error) {
	filename := entry.Name

	var text []byte
	if b, err := io.ReadAll(entry.Reader); err == nil {
		text = b
	}

	now := time.Now()
	slug := shared.SanitizeFileExt(filename)
	fileSize := binary.Size(text)
	shasum := shared.Shasum(text)

	nextPost := db.Post{
		Filename:  filename,
		Slug:      slug,
		PublishAt: &now,
		Text:      string(text),
		MimeType:  http.DetectContentType(text),
		FileSize:  fileSize,
		Shasum:    shasum,
	}

	ext := path.Ext(filename)
	// DetectContentType does not detect markdown
	if ext == ".md" {
		nextPost.MimeType = "text/markdown; charset=UTF-8"
	}

	post, err := h.DBPool.FindPostWithSlug(
		nextPost.Slug,
		h.User.ID,
		h.Cfg.Space,
	)
	if err != nil {
		h.Cfg.Logger.Infof("unable to load post (%s), continuing", nextPost.Filename)
		h.Cfg.Logger.Info(err)
	}

	metadata := filehandlers.PostMetaData{
		Post:      &nextPost,
		User:      h.User,
		FileEntry: entry,
		Cur:       post,
	}

	if post != nil {
		metadata.Post.PublishAt = post.PublishAt
	}

	if metadata.MimeType == mdMime {
		err := h.writeMd(&metadata)
		if err != nil {
			return "", err
		}
	} else {
		err := h.writeImg(&metadata)
		if err != nil {
			return "", err
		}
	}

	url := h.Cfg.FullPostURL(
		h.User.Name,
		metadata.Slug,
		h.Cfg.IsSubdomains(),
		true,
	)
	return url, nil
}
