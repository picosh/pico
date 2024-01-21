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
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/cms/util"
	"github.com/picosh/send/send/utils"
	"go.uber.org/zap"
)

type ctxUserKey struct{}
type ctxFeatureFlagKey struct{}

func getUser(s ssh.Session) (*db.User, error) {
	user := s.Context().Value(ctxUserKey{}).(*db.User)
	if user == nil {
		return user, fmt.Errorf("user not set on `ssh.Context()` for connection")
	}
	return user, nil
}

func getFeatureFlag(s ssh.Session) (*db.FeatureFlag, error) {
	ff := s.Context().Value(ctxFeatureFlagKey{}).(*db.FeatureFlag)
	if ff.Name == "" {
		return ff, fmt.Errorf("feature flag not set on `ssh.Context()` for connection")
	}
	return ff, nil
}

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

	h.Cfg.Logger.Infof("(%s) is empty, removing record (%s)", data.Filename, data.Cur.ID)
	err := h.DBPool.RemovePosts([]string{data.Cur.ID})
	if err != nil {
		h.Cfg.Logger.Errorf("error for %s: %v", data.Filename, err)
		return fmt.Errorf("error for %s: %v", data.Filename, err)
	}

	return nil
}

func (h *UploadImgHandler) GetLogger() *zap.SugaredLogger {
	return h.Cfg.Logger
}

func (h *UploadImgHandler) Read(s ssh.Session, entry *utils.FileEntry) (os.FileInfo, utils.ReaderAtCloser, error) {
	user, err := getUser(s)
	if err != nil {
		return nil, nil, err
	}

	cleanFilename := filepath.Base(entry.Filepath)

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

	contents, _, _, err := h.Storage.GetFile(bucket, post.Filename)
	if err != nil {
		return nil, nil, err
	}

	reader := shared.NewAllReaderAt(contents)

	return fileInfo, reader, nil
}

func (h *UploadImgHandler) List(s ssh.Session, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	var fileList []os.FileInfo
	user, err := getUser(s)
	if err != nil {
		return fileList, err
	}
	cleanFilename := filepath.Base(fpath)

	var post *db.Post
	var posts []*db.Post

	if cleanFilename == "" || cleanFilename == "." || cleanFilename == "/" {
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

	ff, _ := h.DBPool.FindFeatureForUser(user.ID, "imgs")
	// imgs.sh is a free service so users might not have a feature flag
	// in which case we set sane defaults
	if ff == nil {
		ff = &db.FeatureFlag{
			Data: db.FeatureFlagData{},
		}
	}
	s.Context().SetValue(ctxFeatureFlagKey{}, ff)

	s.Context().SetValue(ctxUserKey{}, user)
	h.Cfg.Logger.Infof("(%s) attempting to upload files to (%s)", user.Name, h.Cfg.Space)
	return nil
}

func (h *UploadImgHandler) Write(s ssh.Session, entry *utils.FileEntry) (string, error) {
	user, err := getUser(s)
	if err != nil {
		h.Cfg.Logger.Error(err)
		return "", err
	}

	filename := filepath.Base(entry.Filepath)

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

	ext := filepath.Ext(filename)
	// DetectContentType does not detect markdown
	if ext == ".md" {
		nextPost.MimeType = "text/markdown; charset=UTF-8"
		// DetectContentType does not detect image/svg
	} else if ext == ".svg" {
		nextPost.MimeType = "image/svg+xml"
	}

	post, err := h.DBPool.FindPostWithFilename(
		nextPost.Filename,
		user.ID,
		h.Cfg.Space,
	)
	if err != nil {
		h.Cfg.Logger.Infof("(%s) unable to find image (%s), continuing", nextPost.Filename, err)
	}

	featureFlag, err := getFeatureFlag(s)
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
		h.Cfg.Logger.Error(err)
		return "", err
	}

	totalFileSize, err := h.DBPool.FindTotalSizeForUser(user.ID)
	if err != nil {
		h.Cfg.Logger.Error(err)
		return "", err
	}

	curl := shared.NewCreateURL(h.Cfg)
	url := h.Cfg.FullPostURL(
		curl,
		user.Name,
		metadata.Slug,
	)
	maxSize := int(h.calcStorageMax(featureFlag))
	str := fmt.Sprintf(
		"%s (space: %.2f/%.2fGB, %.2f%%)",
		url,
		shared.BytesToGB(totalFileSize),
		shared.BytesToGB(maxSize),
		(float32(totalFileSize)/float32(maxSize))*100,
	)
	return str, nil
}
