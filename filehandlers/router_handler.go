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
	"github.com/picosh/send/utils"
)

type ReadWriteHandler interface {
	Write(ssh.Session, *utils.FileEntry) (string, error)
	Read(ssh.Session, *utils.FileEntry) (os.FileInfo, utils.ReaderAtCloser, error)
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
	if entry.Mode.IsDir() {
		return "", os.ErrInvalid
	}

	handler, err := r.findHandler(entry)
	if err != nil {
		return "", err
	}
	return handler.Write(s, entry)
}

func (r *FileHandlerRouter) Delete(s ssh.Session, entry *utils.FileEntry) error {
	handler, err := r.findHandler(entry)
	if err != nil {
		return err
	}
	return handler.Delete(s, entry)
}

func (r *FileHandlerRouter) Read(s ssh.Session, entry *utils.FileEntry) (os.FileInfo, utils.ReaderAtCloser, error) {
	handler, err := r.findHandler(entry)
	if err != nil {
		return nil, nil, err
	}
	return handler.Read(s, entry)
}

func BaseList(s ssh.Session, fpath string, isDir bool, recursive bool, spaces []string, dbpool db.DB) ([]os.FileInfo, error) {
	var fileList []os.FileInfo
	user, err := shared.GetUser(s.Context())
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

func (r *FileHandlerRouter) List(s ssh.Session, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	return BaseList(s, fpath, isDir, recursive, r.Spaces, r.DBPool)
}

func (r *FileHandlerRouter) GetLogger() *slog.Logger {
	return r.Cfg.Logger
}

func (r *FileHandlerRouter) Validate(s ssh.Session) error {
	user, err := shared.GetUser(s.Context())
	if err != nil {
		return err
	}

	r.Cfg.Logger.Info(
		"attempting to upload files",
		"user", user.Name,
		"space", r.Cfg.Space,
	)
	return nil
}
