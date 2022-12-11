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

	"github.com/gliderlabs/ssh"
	exifremove "github.com/neurosnap/go-exif-remove"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/imgs/storage"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/cms/util"
	"github.com/picosh/pico/wish/send/utils"
	"golang.org/x/exp/slices"
)

var KB = 1024
var MB = KB * 1024
var GB = MB * 1024
var maxSize = 1 * GB
var maxImgSize = 10 * MB

type ctxUserKey struct{}

func getUser(s ssh.Session) (*db.User, error) {
	user := s.Context().Value(ctxUserKey{}).(*db.User)
	if user == nil {
		return user, fmt.Errorf("user not set on `ssh.Context()` for connection")
	}
	return user, nil
}

type PostMetaData struct {
	*db.Post
	OrigText  []byte
	Cur       *db.Post
	Tags      []string
	User      *db.User
	FileEntry *utils.FileEntry
}

type UploadImgHandler struct {
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
	user, err := getUser(s)
	if err != nil {
		return nil, nil, err
	}

	cleanFilename := strings.ReplaceAll(filename, "/", "")

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

	bucket, err := h.Storage.GetBucket(user.ID)
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
	user, err := getUser(s)
	if err != nil {
		return fileList, err
	}
	cleanFilename := strings.ReplaceAll(filename, "/", "")

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

		posts, err = h.DBPool.FindAllPostsForUser(user.ID, h.Cfg.Space)
	} else {
		post, err = h.DBPool.FindPostWithFilename(cleanFilename, user.ID, h.Cfg.Space)

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

	s.Context().SetValue(ctxUserKey{}, user)
	h.Cfg.Logger.Infof("(%s) attempting to upload files to (%s)", user.Name, h.Cfg.Space)
	return nil
}

func (h *UploadImgHandler) Write(s ssh.Session, entry *utils.FileEntry) (string, error) {
	user, err := getUser(s)
	if err != nil {
		return "", err
	}

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
			if len(noExifBytes) == 0 {
				h.Cfg.Logger.Infof("(%s) silently failed to strip exif data", filename)
			} else {
				text = noExifBytes
				h.Cfg.Logger.Infof("(%s) stripped exif data", filename)
			}
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
		user.ID,
		h.Cfg.Space,
	)
	if err != nil {
		h.Cfg.Logger.Infof("(%s) unable to find post (%s), continuing", nextPost.Filename, err)
	}

	metadata := PostMetaData{
		OrigText:  text,
		Post:      &nextPost,
		User:      user,
		FileEntry: entry,
		Cur:       post,
	}

	if post != nil {
		metadata.Post.PublishAt = post.PublishAt
	}

	err = h.writeImg(s, &metadata)
	if err != nil {
		return "", err
	}

	curl := shared.NewCreateURL(h.Cfg)
	url := h.Cfg.FullPostURL(
		curl,
		user.Name,
		metadata.Slug,
	)
	return url, nil
}
