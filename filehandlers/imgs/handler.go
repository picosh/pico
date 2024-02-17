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
	"github.com/picosh/send/send/utils"
)

var Space = "imgs"

type PostMetaData struct {
	*db.Post
	OrigText []byte
	Cur      *db.Post
	Tags     []string
	User     *db.User
	*utils.FileEntry
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

func (h *UploadImgHandler) removePost(data *PostMetaData) error {
	// skip empty files from being added to db
	if data.Post == nil {
		h.Cfg.Logger.Info("file is empty, skipping record", "filename", data.Filename)
		return nil
	}

	h.Cfg.Logger.Info("file is empty, removing record", "filename", data.Filename, "recordId", data.Cur.ID)
	err := h.DBPool.RemovePosts([]string{data.Cur.ID})
	if err != nil {
		h.Cfg.Logger.Error(err.Error(), "filename", data.Filename)
		return fmt.Errorf("error for %s: %v", data.Filename, err)
	}

	return nil
}

func (h *UploadImgHandler) Read(s ssh.Session, entry *utils.FileEntry) (os.FileInfo, utils.ReaderAtCloser, error) {
	user, err := util.GetUser(s)
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

	fileInfo := &utils.VirtualFile{
		FName:    post.Filename,
		FIsDir:   false,
		FSize:    int64(post.FileSize),
		FModTime: *post.UpdatedAt,
	}

	bucket, err := h.Storage.GetBucket(user.ID)
	if err != nil {
		return nil, nil, err
	}

	contents, _, _, err := h.Storage.GetObject(bucket, post.Filename)
	if err != nil {
		return nil, nil, err
	}

	reader := pobj.NewAllReaderAt(contents)

	return fileInfo, reader, nil
}

func (h *UploadImgHandler) Write(s ssh.Session, entry *utils.FileEntry) (string, error) {
	user, err := util.GetUser(s)
	if err != nil {
		h.Cfg.Logger.Error(err.Error())
		return "", err
	}

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
				h.Cfg.Logger.Info("file silently failed to strip exif data", "filename", filename)
			} else {
				text = noExifBytes
				h.Cfg.Logger.Info("stripped exif data", "filename", filename)
			}
		} else {
			h.Cfg.Logger.Error(err.Error())
		}
	}

	now := time.Now()
	fileSize := binary.Size(text)
	shasum := shared.Shasum(text)
	slug := shared.SanitizeFileExt(filename)

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
		h.Cfg.Logger.Info("unable to find image, continuing", "filename", nextPost.Filename, "err", err.Error())
	}

	featureFlag, err := util.GetFeatureFlag(s)
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
		h.Cfg.Logger.Error(err.Error())
		return "", err
	}

	totalFileSize, err := h.DBPool.FindTotalSizeForUser(user.ID)
	if err != nil {
		h.Cfg.Logger.Error(err.Error())
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
		shared.BytesToGB(totalFileSize),
		shared.BytesToGB(maxSize),
		(float32(totalFileSize)/float32(maxSize))*100,
	)
	return str, nil
}
