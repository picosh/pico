package uploadassets

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
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/cms/util"
	"github.com/picosh/pico/wish/send/utils"
)

var KB = 1024
var MB = KB * 1024
var GB = MB * 1024
var maxSize = 1 * GB
var maxAssetSize = 10 * MB

func bytesToGB(size int) float32 {
	return (((float32(size) / 1024) / 1024) / 1024)
}

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

type UploadAssetHandler struct {
	DBPool  db.DB
	Cfg     *shared.ConfigSite
	Storage storage.ObjectStorage
}

func NewUploadAssetHandler(dbpool db.DB, cfg *shared.ConfigSite, storage storage.ObjectStorage) *UploadAssetHandler {
	return &UploadAssetHandler{
		DBPool:  dbpool,
		Cfg:     cfg,
		Storage: storage,
	}
}

func (h *UploadAssetHandler) removePost(data *PostMetaData) error {
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

func (h *UploadAssetHandler) Read(s ssh.Session, filename string) (os.FileInfo, io.ReaderAt, error) {
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

	bucket, err := h.Storage.GetBucket(shared.GetAssetBucketName(user.ID))
	if err != nil {
		return nil, nil, err
	}

	fname := shared.GetAssetFileName(post.Path, post.Filename)
	contents, err := h.Storage.GetFile(bucket, fname)
	if err != nil {
		return nil, nil, err
	}

	return fileInfo, contents, nil
}

func (h *UploadAssetHandler) List(s ssh.Session, filename string) ([]os.FileInfo, error) {
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

func (h *UploadAssetHandler) Validate(s ssh.Session) error {
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

func (h *UploadAssetHandler) Write(s ssh.Session, entry *utils.FileEntry) (string, error) {
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

	now := time.Now()
	slug := shared.SanitizeFileExt(filename)
	fileSize := binary.Size(text)
	shasum := shared.Shasum(text)

	nextPost := db.Post{
		Path:      entry.Path,
		Filename:  filename,
		Slug:      slug,
		PublishAt: &now,
		Text:      string(text),
		MimeType:  mimeType,
		FileSize:  fileSize,
		Shasum:    shasum,
	}

	ext := path.Ext(filename)
	fmt.Println(ext)
	if ext == ".svg" {
		nextPost.MimeType = "image/svg+xml"
	} else if ext == ".css" {
		nextPost.MimeType = "text/css"
	} else if ext == ".js" {
		nextPost.MimeType = "text/javascript"
	} else if ext == ".ico" {
		nextPost.MimeType = "image/x-icon"
	} else if ext == ".pdf" {
		nextPost.MimeType = "application/pdf"
	}

	post, err := h.DBPool.FindPostWithPath(
		nextPost.Path,
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

	err = h.writeAsset(s, &metadata)
	if err != nil {
		return "", err
	}

	totalFileSize, err := h.DBPool.FindTotalSizeForUser(user.ID)
	if err != nil {
		return "", err
	}

	curl := shared.NewCreateURL(h.Cfg)
	preUrl := h.Cfg.FullPostURL(
		curl,
		user.Name,
		metadata.Path,
	)
	url := fmt.Sprintf("%s/%s", preUrl, metadata.Filename)
	str := fmt.Sprintf(
		"%s (space: %.2f/%.2fGB, %.2f%%)",
		url,
		bytesToGB(totalFileSize),
		bytesToGB(maxSize),
		(float32(totalFileSize)/float32(maxSize))*100,
	)
	return str, nil
}
