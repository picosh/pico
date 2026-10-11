package filehandlers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
)

type ReadWriteHandler interface {
	List(s *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error)
	Write(*pssh.SSHServerConnSession, *utils.FileEntry) (string, error)
	Read(*pssh.SSHServerConnSession, *utils.FileEntry) (os.FileInfo, utils.ReadAndReaderAtCloser, error)
	Delete(*pssh.SSHServerConnSession, *utils.FileEntry) error
}

type FileHandlerRouter struct {
	FileMap map[string]ReadWriteHandler
	Cfg     *shared.ConfigSite
}

var _ utils.CopyFromClientHandler = &FileHandlerRouter{}      // Verify implementation
var _ utils.CopyFromClientHandler = (*FileHandlerRouter)(nil) // Verify implementation

func NewFileHandlerRouter(cfg *shared.ConfigSite, mapper map[string]ReadWriteHandler) *FileHandlerRouter {
	return &FileHandlerRouter{
		Cfg:     cfg,
		FileMap: mapper,
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

func (r *FileHandlerRouter) Write(s *pssh.SSHServerConnSession, entry *utils.FileEntry) (string, error) {
	if entry.Mode.IsDir() {
		return "", os.ErrInvalid
	}

	handler, err := r.findHandler(entry.Filepath)
	if err != nil {
		return "", err
	}
	return handler.Write(s, entry)
}

func (r *FileHandlerRouter) ValidateWrite(s *pssh.SSHServerConnSession, entry *utils.FileEntry) error {
	handler, err := r.findHandler(entry.Filepath)
	if err != nil {
		return err
	}
	if v, ok := handler.(utils.WriteValidator); ok {
		return v.ValidateWrite(s, entry)
	}
	return nil
}

func (r *FileHandlerRouter) Delete(s *pssh.SSHServerConnSession, entry *utils.FileEntry) error {
	handler, err := r.findHandler(entry.Filepath)
	if err != nil {
		return err
	}
	return handler.Delete(s, entry)
}

// Rename moves a file in place when one handler serves both names.
func (r *FileHandlerRouter) Rename(s *pssh.SSHServerConnSession, from, to *utils.FileEntry) error {
	handler, err := r.findHandler(from.Filepath)
	if err != nil {
		return err
	}
	target, err := r.findHandler(to.Filepath)
	if err != nil {
		return err
	}
	renamer, ok := handler.(utils.FileRenamer)
	if !ok || handler != target {
		return errors.ErrUnsupported
	}
	return renamer.Rename(s, from, to)
}

func (r *FileHandlerRouter) Read(s *pssh.SSHServerConnSession, entry *utils.FileEntry) (os.FileInfo, utils.ReadAndReaderAtCloser, error) {
	handler, err := r.findHandler(entry.Filepath)
	if err != nil {
		return nil, nil, err
	}
	return handler.Read(s, entry)
}

func (r *FileHandlerRouter) List(s *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
	files := []os.FileInfo{}
	// Several extensions can share a handler, and handlers can list the
	// same entries, such as the root directory.
	listed := map[ReadWriteHandler]bool{}
	seen := map[string]bool{}
	for _, handler := range r.FileMap {
		if listed[handler] {
			continue
		}
		listed[handler] = true

		ff, err := handler.List(s, fpath, isDir, recursive)
		if err != nil {
			r.GetLogger(s).Error("handler list", "err", err)
			continue
		}
		for _, f := range ff {
			key := fmt.Sprintf("%s:%v", f.Name(), f.IsDir())
			if !seen[key] {
				seen[key] = true
				files = append(files, f)
			}
		}
	}
	return files, nil
}

func (r *FileHandlerRouter) GetLogger(s *pssh.SSHServerConnSession) *slog.Logger {
	return pssh.GetLogger(s)
}

func (r *FileHandlerRouter) Validate(s *pssh.SSHServerConnSession) error {
	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

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

func BaseList(s *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool, spaces []string, dbpool db.PostDB) ([]os.FileInfo, error) {
	var fileList []os.FileInfo
	logger := pssh.GetLogger(s)
	user := pssh.GetUser(s)

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
			curPosts, e := dbpool.FindAllPostsByUser(user.ID, space)
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
