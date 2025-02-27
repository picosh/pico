package filehandlers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish"
	"github.com/picosh/send/utils"
)

type ReadWriteHandler interface {
	List(s ssh.Session, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error)
	Write(ssh.Session, *utils.FileEntry) (string, error)
	Read(ssh.Session, *utils.FileEntry) (os.FileInfo, utils.ReadAndReaderAtCloser, error)
	Delete(ssh.Session, *utils.FileEntry) error
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

func (r *FileHandlerRouter) findHandler(fp string) (ReadWriteHandler, error) {
	fext := filepath.Ext(fp)
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
	if entry.Mode.IsDir() {
		return "", os.ErrInvalid
	}

	handler, err := r.findHandler(entry.Filepath)
	if err != nil {
		return "", err
	}
	return handler.Write(s, entry)
}

func (r *FileHandlerRouter) Delete(s ssh.Session, entry *utils.FileEntry) error {
	handler, err := r.findHandler(entry.Filepath)
	if err != nil {
		return err
	}
	return handler.Delete(s, entry)
}

func (r *FileHandlerRouter) Read(s ssh.Session, entry *utils.FileEntry) (os.FileInfo, utils.ReadAndReaderAtCloser, error) {
	handler, err := r.findHandler(entry.Filepath)
	if err != nil {
		return nil, nil, err
	}
	return handler.Read(s, entry)
}

func (r *FileHandlerRouter) List(s ssh.Session, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	files := []os.FileInfo{}
	for key, handler := range r.FileMap {
		// TODO: hack because we have duplicate keys for .md and .css
		if key == ".css" {
			continue
		}

		ff, err := handler.List(s, fpath, isDir, recursive)
		if err != nil {
			r.GetLogger(s).Error("handler list", "err", err)
			continue
		}
		files = append(files, ff...)
	}
	return files, nil
}

func (r *FileHandlerRouter) GetLogger(s ssh.Session) *slog.Logger {
	return wish.GetLogger(s)
}

func (r *FileHandlerRouter) Validate(s ssh.Session) error {
	logger := wish.GetLogger(s)
	user := wish.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return err
	}

	logger.Info(
		"attempting to upload files",
		"user", user.Name,
		"space", r.Cfg.Space,
	)
	return nil
}

func BaseList(s ssh.Session, fpath string, isDir bool, recursive bool, spaces []string, dbpool db.DB) ([]os.FileInfo, error) {
	var fileList []os.FileInfo
	logger := wish.GetLogger(s)
	user := wish.GetUser(s)

	var err error

	if user == nil {
		err = fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
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

		for _, space := range spaces {
			curPosts, e := dbpool.FindAllPostsForUser(user.ID, space)
			if e != nil {
				err = e
				break
			}
			posts = append(posts, curPosts...)
		}
	} else {
		for _, space := range spaces {
			p, e := dbpool.FindPostWithFilename(cleanFilename, user.ID, space)
			if e != nil {
				err = e
				continue
			}
			post = p
		}

		posts = append(posts, post)
	}

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	for _, post := range posts {
		if post == nil {
			continue
		}

		fileList = append(fileList, &utils.VirtualFile{
			FName:    post.Filename,
			FIsDir:   false,
			FSize:    int64(post.FileSize),
			FModTime: *post.UpdatedAt,
		})
	}

	return fileList, nil
}
