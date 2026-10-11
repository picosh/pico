package prose

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
)

// ReadWriteHandler serves the files with the extensions it is routed.
type ReadWriteHandler interface {
	List(s *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error)
	Write(*pssh.SSHServerConnSession, *sendutils.FileEntry) (string, error)
	Read(*pssh.SSHServerConnSession, *sendutils.FileEntry) (os.FileInfo, sendutils.ReadAndReaderAtCloser, error)
	Delete(*pssh.SSHServerConnSession, *sendutils.FileEntry) error
}

// fileRouter sends each file to the handler for its extension.
type fileRouter struct {
	FileMap map[string]ReadWriteHandler
	Space   string
}

var _ sendutils.CopyFromClientHandler = (*fileRouter)(nil)

func newFileRouter(space string, mapper map[string]ReadWriteHandler) *fileRouter {
	return &fileRouter{
		Space:   space,
		FileMap: mapper,
	}
}

func (r *fileRouter) findHandler(fp string) (ReadWriteHandler, error) {
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

func (r *fileRouter) Write(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) (string, error) {
	if entry.Mode.IsDir() {
		return "", os.ErrInvalid
	}

	handler, err := r.findHandler(entry.Filepath)
	if err != nil {
		return "", err
	}
	return handler.Write(s, entry)
}

func (r *fileRouter) ValidateWrite(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) error {
	handler, err := r.findHandler(entry.Filepath)
	if err != nil {
		return err
	}
	if v, ok := handler.(sendutils.WriteValidator); ok {
		return v.ValidateWrite(s, entry)
	}
	return nil
}

func (r *fileRouter) Delete(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) error {
	handler, err := r.findHandler(entry.Filepath)
	if err != nil {
		return err
	}
	return handler.Delete(s, entry)
}

// Rename moves a file in place when one handler serves both names.
func (r *fileRouter) Rename(s *pssh.SSHServerConnSession, from, to *sendutils.FileEntry) error {
	handler, err := r.findHandler(from.Filepath)
	if err != nil {
		return err
	}
	target, err := r.findHandler(to.Filepath)
	if err != nil {
		return err
	}
	renamer, ok := handler.(sendutils.FileRenamer)
	if !ok || handler != target {
		return errors.ErrUnsupported
	}
	return renamer.Rename(s, from, to)
}

func (r *fileRouter) Read(s *pssh.SSHServerConnSession, entry *sendutils.FileEntry) (os.FileInfo, sendutils.ReadAndReaderAtCloser, error) {
	handler, err := r.findHandler(entry.Filepath)
	if err != nil {
		return nil, nil, err
	}
	return handler.Read(s, entry)
}

func (r *fileRouter) List(s *pssh.SSHServerConnSession, fpath string, isDir bool, recursive bool) ([]os.FileInfo, error) {
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

func (r *fileRouter) GetLogger(s *pssh.SSHServerConnSession) *slog.Logger {
	return pssh.GetLogger(s)
}

func (r *fileRouter) Validate(s *pssh.SSHServerConnSession) error {
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
		"space", r.Space,
	)
	return nil
}
