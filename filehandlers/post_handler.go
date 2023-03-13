package filehandlers

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
	"github.com/picosh/pico/imgs"
	"github.com/picosh/pico/imgs/storage"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/cms/util"
	"github.com/picosh/pico/wish/send/utils"
)

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
	Cur       *db.Post
	Tags      []string
	User      *db.User
	FileEntry *utils.FileEntry
	Aliases   []string
}

type ScpFileHooks interface {
	FileValidate(data *PostMetaData) (bool, error)
	FileMeta(data *PostMetaData) error
}

type ScpUploadHandler struct {
	DBPool    db.DB
	Cfg       *shared.ConfigSite
	Hooks     ScpFileHooks
	ImgClient *imgs.ImgsAPI
}

func NewScpPostHandler(dbpool db.DB, cfg *shared.ConfigSite, hooks ScpFileHooks, st storage.ObjectStorage) *ScpUploadHandler {
	client := imgs.NewImgsAPI(dbpool, st)

	return &ScpUploadHandler{
		DBPool:    dbpool,
		Cfg:       cfg,
		Hooks:     hooks,
		ImgClient: client,
	}
}

func (h *ScpUploadHandler) Read(s ssh.Session, filename string) (os.FileInfo, io.ReaderAt, error) {
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

	return fileInfo, strings.NewReader(post.Text), nil
}

func (h *ScpUploadHandler) List(s ssh.Session, filename string) ([]os.FileInfo, error) {
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

func (h *ScpUploadHandler) Validate(s ssh.Session) error {
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

func (h *ScpUploadHandler) Write(s ssh.Session, entry *utils.FileEntry) (string, error) {
	logger := h.Cfg.Logger
	user, err := getUser(s)
	if err != nil {
		return "", err
	}

	userID := user.ID
	filename := entry.Name

	if shared.IsExtAllowed(filename, h.ImgClient.Cfg.AllowedExt) {
		return h.ImgClient.Upload(s, entry)
	}

	var origText []byte
	if b, err := io.ReadAll(entry.Reader); err == nil {
		origText = b
	}

	mimeType := http.DetectContentType(origText)
	ext := path.Ext(filename)
	// DetectContentType does not detect markdown
	if ext == ".md" {
		mimeType = "text/markdown; charset=UTF-8"
	}

	now := time.Now()
	slug := shared.SanitizeFileExt(filename)
	fileSize := binary.Size(origText)
	shasum := shared.Shasum(origText)

	nextPost := db.Post{
		Filename:  filename,
		Slug:      slug,
		PublishAt: &now,
		Text:      string(origText),
		MimeType:  mimeType,
		FileSize:  fileSize,
		Shasum:    shasum,
	}

	metadata := PostMetaData{
		Post:      &nextPost,
		User:      user,
		FileEntry: entry,
	}

	valid, err := h.Hooks.FileValidate(&metadata)
	if !valid {
		logger.Info(err)
		return "", err
	}

	post, err := h.DBPool.FindPostWithFilename(metadata.Filename, metadata.User.ID, h.Cfg.Space)
	if err != nil {
		logger.Infof("unable to load post (%s), continuing", filename)
		logger.Info(err)
	}

	if post != nil {
		metadata.Cur = post
		metadata.Post.PublishAt = post.PublishAt
	}

	err = h.Hooks.FileMeta(&metadata)
	if err != nil {
		logger.Error(err)
		return "", err
	}

	// if the file is empty we remove it from our database
	if len(origText) == 0 {
		// skip empty files from being added to db
		if post == nil {
			logger.Infof("(%s) is empty, skipping record", filename)
			return "", nil
		}

		err := h.DBPool.RemovePosts([]string{post.ID})
		logger.Infof("(%s) is empty, removing record", filename)
		if err != nil {
			logger.Errorf("error for %s: %v", filename, err)
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	} else if post == nil {
		logger.Infof("(%s) not found, adding record", filename)
		insertPost := db.Post{
			UserID: userID,
			Space:  h.Cfg.Space,

			Data:        metadata.Data,
			Description: metadata.Description,
			Filename:    metadata.Filename,
			FileSize:    metadata.FileSize,
			Hidden:      metadata.Hidden,
			MimeType:    metadata.MimeType,
			PublishAt:   metadata.PublishAt,
			Shasum:      metadata.Shasum,
			Slug:        metadata.Slug,
			Text:        metadata.Text,
			Title:       metadata.Title,
			ExpiresAt:   metadata.ExpiresAt,
		}
		post, err = h.DBPool.InsertPost(&insertPost)
		if err != nil {
			logger.Errorf("error for %s: %v", filename, err)
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}

		if len(metadata.Aliases) > 0 {
			logger.Infof(
				"Found (%s) post aliases, replacing with old aliases",
				strings.Join(metadata.Aliases, ","),
			)
			err = h.DBPool.ReplaceAliasesForPost(metadata.Aliases, post.ID)
			if err != nil {
				logger.Errorf("error for %s: %v", filename, err)
				return "", fmt.Errorf("error for %s: %v", filename, err)
			}
		}

		if len(metadata.Tags) > 0 {
			logger.Infof(
				"Found (%s) post tags, replacing with old tags",
				strings.Join(metadata.Tags, ","),
			)
			err = h.DBPool.ReplaceTagsForPost(metadata.Tags, post.ID)
			if err != nil {
				logger.Errorf("error for %s: %v", filename, err)
				return "", fmt.Errorf("error for %s: %v", filename, err)
			}
		}
	} else {
		if metadata.Text == post.Text {
			logger.Infof("(%s) found, but text is identical, skipping", filename)
			curl := shared.NewCreateURL(h.Cfg)
			return h.Cfg.FullPostURL(curl, user.Name, metadata.Slug), nil
		}

		logger.Infof("(%s) found, updating record", filename)
		updatePost := db.Post{
			ID: post.ID,

			Data:        metadata.Data,
			FileSize:    metadata.FileSize,
			Description: metadata.Description,
			PublishAt:   metadata.PublishAt,
			Slug:        metadata.Slug,
			Shasum:      metadata.Shasum,
			Text:        metadata.Text,
			Title:       metadata.Title,
		}
		_, err = h.DBPool.UpdatePost(&updatePost)
		if err != nil {
			logger.Errorf("error for %s: %v", filename, err)
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}

		logger.Infof(
			"Found (%s) post tags, replacing with old tags",
			strings.Join(metadata.Tags, ","),
		)
		err = h.DBPool.ReplaceTagsForPost(metadata.Tags, post.ID)
		if err != nil {
			logger.Errorf("error for %s: %v", filename, err)
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}

		logger.Infof(
			"Found (%s) post aliases, replacing with old aliases",
			strings.Join(metadata.Aliases, ","),
		)
		err = h.DBPool.ReplaceAliasesForPost(metadata.Aliases, post.ID)
		if err != nil {
			logger.Errorf("error for %s: %v", filename, err)
			return "", fmt.Errorf("error for %s: %v", filename, err)
		}
	}

	curl := shared.NewCreateURL(h.Cfg)
	return h.Cfg.FullPostURL(curl, user.Name, metadata.Slug), nil
}
