package filehandlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/filehandlers/util"
	"github.com/picosh/pico/shared"
	"github.com/picosh/send/send/utils"
	"go.uber.org/zap"
)

type ReadWriteHandler interface {
	Write(ssh.Session, *utils.FileEntry) (string, error)
	Read(ssh.Session, *utils.FileEntry) (os.FileInfo, utils.ReaderAtCloser, error)
}

type FileHandlerRouter struct {
	FileMap map[string]ReadWriteHandler
	Cfg     *shared.ConfigSite
	DBPool  db.DB
	Spaces  []string
}

var _ utils.CopyFromClientHandler = &FileHandlerRouter{}      // Verify implementation
var _ utils.CopyFromClientHandler = (*FileHandlerRouter)(nil) // Verify implementation

func NewFileHandlerRouter(cfg *shared.ConfigSite, dbpool db.DB, mapper map[string]ReadWriteHandler) *FileHandlerRouter {
	return &FileHandlerRouter{
		Cfg:     cfg,
		DBPool:  dbpool,
		FileMap: mapper,
		Spaces:  []string{cfg.Space},
	}
}

func (r *FileHandlerRouter) findHandler(entry *utils.FileEntry) (ReadWriteHandler, error) {
	fext := filepath.Ext(entry.Filepath)
	handler, ok := r.FileMap[fext]
	if !ok {
		hand, hasFallback := r.FileMap["fallback"]
		if !hasFallback {
			return nil, fmt.Errorf("no corresponding handler for file extension: %s", fext)
		}
		handler = hand
	}
	return handler, nil
}

func (r *FileHandlerRouter) Write(s ssh.Session, entry *utils.FileEntry) (string, error) {
	handler, err := r.findHandler(entry)
	if err != nil {
		return "", err
	}
	return handler.Write(s, entry)
}

func (r *FileHandlerRouter) Read(s ssh.Session, entry *utils.FileEntry) (os.FileInfo, utils.ReaderAtCloser, error) {
	handler, err := r.findHandler(entry)
	if err != nil {
		return nil, nil, err
	}
	return handler.Read(s, entry)
}

func (r *FileHandlerRouter) List(s ssh.Session, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	var fileList []os.FileInfo
	user, err := util.GetUser(s)
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

		for _, space := range r.Spaces {
			curPosts, e := r.DBPool.FindAllPostsForUser(user.ID, space)
			if e != nil {
				err = e
				break
			}
			posts = append(posts, curPosts...)
		}
	} else {
		for _, space := range r.Spaces {
			p, e := r.DBPool.FindPostWithFilename(cleanFilename, user.ID, space)
			if e != nil {
				err = e
				continue
			}
			post = p
		}

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

func (r *FileHandlerRouter) GetLogger() *zap.SugaredLogger {
	return r.Cfg.Logger
}

func (r *FileHandlerRouter) Validate(s ssh.Session) error {
	var err error
	key, err := utils.KeyText(s)
	if err != nil {
		return fmt.Errorf("key not found")
	}

	user, err := r.DBPool.FindUserForKey(s.User(), key)
	if err != nil {
		return err
	}

	if user.Name == "" {
		return fmt.Errorf("must have username set")
	}

	ff, _ := r.DBPool.FindFeatureForUser(user.ID, r.Cfg.Space)
	// we have free tiers so users might not have a feature flag
	// in which case we set sane defaults
	if ff == nil {
		ff = db.NewFeatureFlag(
			user.ID,
			r.Cfg.Space,
			r.Cfg.MaxSize,
			r.Cfg.MaxAssetSize,
		)
	}
	// this is jank
	ff.Data.StorageMax = ff.FindStorageMax(r.Cfg.MaxSize)
	ff.Data.FileMax = ff.FindFileMax(r.Cfg.MaxAssetSize)

	util.SetUser(s, user)
	util.SetFeatureFlag(s, ff)

	r.Cfg.Logger.Infof("(%s) attempting to upload files to (%s)", user.Name, r.Cfg.Space)
	return nil
}
