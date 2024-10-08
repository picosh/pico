package uploadimgs

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"slices"

	"github.com/charmbracelet/ssh"
	exifremove "github.com/neurosnap/go-exif-remove"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/filehandlers/util"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pobj"
	sendutils "github.com/picosh/send/utils"
	"github.com/picosh/utils"
)

var Space = "imgs"

type PostMetaData struct {
	*db.Post
	OrigText []byte
	Cur      *db.Post
	Tags     []string
	User     *db.User
	*sendutils.FileEntry
	FeatureFlag *db.FeatureFlag
}

type UploadImgHandler struct {
	DBPool  db.DB
	Cfg     *shared.ConfigSite
	Storage storage.StorageServe
}

func NewUploadImgHandler(dbpool db.DB, cfg *shared.ConfigSite, storage storage.StorageServe) *UploadImgHandler {
	return &UploadImgHandler{
		DBPool:  dbpool,
		Cfg:     cfg,
		Storage: storage,
	}
}

func (h *UploadImgHandler) Read(s ssh.Session, entry *sendutils.FileEntry) (os.FileInfo, sendutils.ReaderAtCloser, error) {
	user, err := util.GetUser(s.Context())
	if err != nil {
		return nil, nil, err
	}

	cleanFilename := filepath.Base(entry.Filepath)

	if cleanFilename == "" || cleanFilename == "." {
		return nil, nil, os.ErrNotExist
	}

	post, err := h.DBPool.FindPostWithFilename(cleanFilename, user.ID, Space)
	if err != nil {
		return nil, nil, err
	}

	fileInfo := &sendutils.VirtualFile{
		FName:    post.Filename,
		FIsDir:   false,
		FSize:    int64(post.FileSize),
		FModTime: *post.UpdatedAt,
	}

	bucket, err := h.Storage.GetBucket(user.ID)
	if err != nil {
		return nil, nil, err
	}

	contents, _, err := h.Storage.GetObject(bucket, post.Filename)
	if err != nil {
		return nil, nil, err
	}

	reader := pobj.NewAllReaderAt(contents)

	return fileInfo, reader, nil
}

func (h *UploadImgHandler) Write(s ssh.Session, entry *sendutils.FileEntry) (string, error) {
	logger := h.Cfg.Logger
	user, err := util.GetUser(s.Context())
	if err != nil {
		logger.Error("could not get user from ctx", "err", err.Error())
		return "", err
	}
	logger = shared.LoggerWithUser(logger, user)

	filename := filepath.Base(entry.Filepath)

	var text []byte
	if b, err := io.ReadAll(entry.Reader); err == nil {
		text = b
	}
	mimeType := http.DetectContentType(text)
	ext := filepath.Ext(filename)
	if ext == ".svg" {
		mimeType = "image/svg+xml"
	}
	// strip exif data
	if slices.Contains([]string{"image/png", "image/jpg", "image/jpeg"}, mimeType) {
		noExifBytes, err := exifremove.Remove(text)
		if err == nil {
			if len(noExifBytes) == 0 {
				logger.Info("file silently failed to strip exif data", "filename", filename)
			} else {
				text = noExifBytes
				logger.Info("stripped exif data", "filename", filename)
			}
		} else {
			logger.Error("could not strip exif data", "err", err.Error())
		}
	}

	now := time.Now()
	fileSize := binary.Size(text)
	shasum := utils.Shasum(text)
	slug := utils.SanitizeFileExt(filename)

	nextPost := db.Post{
		Filename:  filename,
		Slug:      slug,
		PublishAt: &now,
		Text:      string(text),
		MimeType:  mimeType,
		FileSize:  fileSize,
		Shasum:    shasum,
	}

	post, err := h.DBPool.FindPostWithFilename(
		nextPost.Filename,
		user.ID,
		Space,
	)
	if err != nil {
		logger.Info("unable to find image, continuing", "filename", nextPost.Filename, "err", err.Error())
	}

	featureFlag, err := util.GetFeatureFlag(s.Context())
	if err != nil {
		return "", err
	}
	metadata := PostMetaData{
		OrigText:    text,
		Post:        &nextPost,
		User:        user,
		FileEntry:   entry,
		Cur:         post,
		FeatureFlag: featureFlag,
	}

	if post != nil {
		metadata.Post.PublishAt = post.PublishAt
	}

	err = h.writeImg(s, &metadata)
	if err != nil {
		logger.Error("could not write img", "err", err.Error())
		return "", err
	}

	totalFileSize, err := h.DBPool.FindTotalSizeForUser(user.ID)
	if err != nil {
		logger.Error("could not find total storage size for user", "err", err.Error())
		return "", err
	}

	curl := shared.NewCreateURL(h.Cfg)
	url := h.Cfg.FullPostURL(
		curl,
		user.Name,
		metadata.Filename,
	)
	maxSize := int(featureFlag.Data.StorageMax)
	str := fmt.Sprintf(
		"%s (space: %.2f/%.2fGB, %.2f%%)",
		url,
		utils.BytesToGB(totalFileSize),
		utils.BytesToGB(maxSize),
		(float32(totalFileSize)/float32(maxSize))*100,
	)
	return str, nil
}

func (h *UploadImgHandler) Delete(s ssh.Session, entry *sendutils.FileEntry) error {
	user, err := util.GetUser(s.Context())
	if err != nil {
		return err
	}

	filename := filepath.Base(entry.Filepath)

	logger := h.Cfg.Logger
	logger = shared.LoggerWithUser(logger, user)
	logger = logger.With(
		"filename", filename,
	)

	post, err := h.DBPool.FindPostWithFilename(
		filename,
		user.ID,
		Space,
	)
	if err != nil {
		logger.Info("unable to find image, continuing", "err", err.Error())
		return err
	}

	err = h.DBPool.RemovePosts([]string{post.ID})
	if err != nil {
		logger.Error("error removing image", "error", err)
		return fmt.Errorf("error for %s: %v", filename, err)
	}

	bucket, err := h.Storage.UpsertBucket(user.ID)
	if err != nil {
		return err
	}

	err = h.Storage.DeleteObject(bucket, filename)
	if err != nil {
		return err
	}

	logger.Info("deleting image")

	return nil
}
