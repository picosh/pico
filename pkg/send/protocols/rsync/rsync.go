package rsync

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/rsync"
	"github.com/picosh/pico/pkg/send/utils"
)

// keepDirName marks an otherwise empty directory in object storage.
const keepDirName = "._pico_keep_dir"

// storageFS exposes a CopyFromClientHandler as an rsync.FS. Handlers take
// absolute paths and differ in how they name listing results, so this
// normalizes both directions.
type storageFS struct {
	session *pssh.SSHServerConnSession
	handler utils.CopyFromClientHandler
}

func abs(name string) string { return "/" + strings.TrimPrefix(name, "/") }

// isSelf reports whether a listing entry stands for the listed path itself
// rather than something below it.
func isSelf(info os.FileInfo) bool {
	switch info.Name() {
	case "", ".", "/":
		return info.IsDir()
	}
	return false
}

func (s *storageFS) Stat(name string) (rsync.FileInfo, error) {
	if name == "" {
		return rsync.FileInfo{IsDir: true}, nil
	}
	entries, err := s.handler.List(s.session, abs(name), false, false)
	if err != nil {
		return rsync.FileInfo{}, err
	}
	for _, e := range entries {
		if isSelf(e) {
			return rsync.FileInfo{Name: name, ModTime: e.ModTime(), IsDir: true}, nil
		}
	}
	switch {
	case len(entries) == 0:
		return rsync.FileInfo{}, fmt.Errorf("%s: %w", name, fs.ErrNotExist)
	case len(entries) == 1 && !entries[0].IsDir() && path.Base(entries[0].Name()) == path.Base(name):
		e := entries[0]
		return rsync.FileInfo{Name: name, Size: e.Size(), ModTime: e.ModTime()}, nil
	default:
		// The handler listed the contents of a directory.
		return rsync.FileInfo{Name: name, IsDir: true}, nil
	}
}

func (s *storageFS) ReadDir(dir string, recursive bool) ([]rsync.FileInfo, error) {
	entries, err := s.handler.List(s.session, abs(dir), true, recursive)
	if err != nil {
		return nil, err
	}
	out := make([]rsync.FileInfo, 0, len(entries))
	for _, e := range entries {
		if isSelf(e) {
			continue
		}
		name := strings.Trim(e.Name(), "/")
		if name == "" || path.Base(name) == keepDirName {
			continue
		}
		out = append(out, rsync.FileInfo{Name: name, Size: e.Size(), ModTime: e.ModTime(), IsDir: e.IsDir()})
	}
	return out, nil
}

func (s *storageFS) Open(name string) (rsync.File, rsync.FileInfo, error) {
	info, r, err := s.handler.Read(s.session, &utils.FileEntry{Filepath: abs(name)})
	if err != nil {
		return nil, rsync.FileInfo{}, err
	}
	if r == nil {
		return nil, rsync.FileInfo{}, fmt.Errorf("%s: %w", name, fs.ErrNotExist)
	}
	return r, rsync.FileInfo{Name: name, Size: info.Size(), ModTime: info.ModTime()}, nil
}

func (s *storageFS) Put(name string, info rsync.FileInfo, r io.Reader) (string, error) {
	return s.handler.Write(s.session, &utils.FileEntry{
		Filepath: abs(name),
		Mode:     fs.FileMode(0600),
		Size:     info.Size,
		Mtime:    info.ModTime.Unix(),
		Atime:    info.ModTime.Unix(),
		Reader:   r,
	})
}

func (s *storageFS) Remove(name string, isDir bool) error {
	return s.handler.Delete(s.session, &utils.FileEntry{Filepath: abs(name)})
}

// Middleware serves rsync clients ("rsync --server ...") from the handler's
// storage.
func Middleware(writeHandler utils.CopyFromClientHandler) pssh.SSHServerMiddleware {
	return func(sshHandler pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(session *pssh.SSHServerConnSession) error {
			cmd := session.Command()
			if len(cmd) == 0 || cmd[0] != "rsync" {
				return sshHandler(session)
			}

			logger := writeHandler.GetLogger(session).With(
				"rsync", true,
				"cmd", cmd,
			)

			defer func() {
				if r := recover(); r != nil {
					logger.Error("error running rsync middleware", "err", r)
					_, _ = session.Stderr().Write([]byte("ERROR: error running rsync middleware, check the flags you are using\r\n"))
				}
			}()

			err := rsync.Serve(rsync.Config{
				FS:     &storageFS{session: session, handler: writeHandler},
				Logger: logger,
				Stderr: session.Stderr(),
			}, session, cmd[1:])
			if err == nil {
				return nil
			}

			// The client has already been told what went wrong; what is
			// left is to hand it rsync's exit code.
			code := 1
			var exit *rsync.ExitError
			if errors.As(err, &exit) {
				code = exit.Code
			}
			logger.Error("rsync transfer failed", "err", err, "code", code)
			_ = session.Exit(code)
			_ = session.Close()
			return nil
		}
	}
}
