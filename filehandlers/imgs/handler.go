package uploadimgs

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/imgs/storage"
	"git.sr.ht/~erock/pico/shared"
	"git.sr.ht/~erock/pico/wish/cms/util"
	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/gliderlabs/ssh"
	exifremove "github.com/scottleedavis/go-exif-remove"
	"golang.org/x/exp/slices"
)

var GB = 1024 * 1024 * 1024
var maxSize = 2 * GB

type PostMetaData struct {
	*db.Post
	OrigText  []byte
	Cur       *db.Post
	Tags      []string
	User      *db.User
	FileEntry *utils.FileEntry
}

type UploadImgHandler struct {
	User    *db.User
	DBPool  db.DB
	Cfg     *shared.ConfigSite
	Storage storage.ObjectStorage
}

func NewUploadImgHandler(dbpool db.DB, cfg *shared.ConfigSite, storage storage.ObjectStorage) *UploadImgHandler {
	return &UploadImgHandler{
		DBPool:  dbpool,
		Cfg:     cfg,
		Storage: storage,
	}
}

func (h *UploadImgHandler) removePost(data *PostMetaData) error {
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

func (h *UploadImgHandler) Read(s ssh.Session, filename string) (os.FileInfo, io.ReaderAt, error) {
	cleanFilename := strings.ReplaceAll(filename, "/", "")

	if cleanFilename == "" || cleanFilename == "." {
		return nil, nil, os.ErrNotExist
	}

	post, err := h.DBPool.FindPostWithFilename(cleanFilename, h.User.ID, h.Cfg.Space)
	if err != nil {
		return nil, nil, err
	}

	fileInfo := &utils.VirtualFile{
		FName:    post.Filename,
		FIsDir:   false,
		FSize:    int64(post.FileSize),
		FModTime: *post.UpdatedAt,
	}

	bucket, err := h.Storage.GetBucket(h.User.ID)
	if err != nil {
		return nil, nil, err
	}

	contents, err := h.Storage.GetFile(bucket, post.Filename)
	if err != nil {
		return nil, nil, err
	}

	return fileInfo, contents, nil
}

func (h *UploadImgHandler) List(s ssh.Session, filename string) ([]os.FileInfo, error) {
	var fileList []os.FileInfo
	cleanFilename := strings.ReplaceAll(filename, "/", "")

	var err error
	var post *db.Post
	var posts []*db.Post

	if cleanFilename == "" || cleanFilename == "." {
		name := cleanFilename
		if name == "" {
			name = "/"
		}

		fileList = append(fileList, &utils.VirtualFile{
			FName:  name,
			FIsDir: true,
		})

		posts, err = h.DBPool.FindAllPostsForUser(h.User.ID, h.Cfg.Space)
	} else {
		post, err = h.DBPool.FindPostWithFilename(cleanFilename, h.User.ID, h.Cfg.Space)

		posts = append(posts, post)
	}

	if err != nil {
		return nil, err
	}

	for _, post := range posts {
		fileList = append(fileList, &utils.VirtualFile{
			FName:    post.Filename,
			FIsDir:   false,
			FSize:    int64(post.FileSize),
			FModTime: *post.UpdatedAt,
		})
	}

	return fileList, nil
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
	mimeType := http.DetectContentType(text)
	// strip exif data
	if slices.Contains([]string{"image/png", "image/jpg", "image/jpeg"}, mimeType) {
		noExifBytes, err := exifremove.Remove(text)
		if err == nil {
			text = noExifBytes
			h.Cfg.Logger.Infof("(%s) stripped exif data", filename)
		} else {
			h.Cfg.Logger.Error(err)
		}
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
		MimeType:  mimeType,
		FileSize:  fileSize,
		Shasum:    shasum,
	}

	ext := path.Ext(filename)
	// DetectContentType does not detect markdown
	if ext == ".md" {
		nextPost.MimeType = "text/markdown; charset=UTF-8"
	}

	post, err := h.DBPool.FindPostWithFilename(
		nextPost.Filename,
		h.User.ID,
		h.Cfg.Space,
	)
	if err != nil {
		h.Cfg.Logger.Infof("unable to load post (%s), continuing", nextPost.Filename)
		h.Cfg.Logger.Info(err)
	}

	metadata := PostMetaData{
		OrigText:  text,
		Post:      &nextPost,
		User:      h.User,
		FileEntry: entry,
		Cur:       post,
	}

	if post != nil {
		metadata.Post.PublishAt = post.PublishAt
	}

	err = h.writeImg(&metadata)
	if err != nil {
		return "", err
	}

	url := h.Cfg.FullPostURL(
		h.User.Name,
		metadata.Slug,
		h.Cfg.IsSubdomains(),
		true,
	)
	return url, nil
}
