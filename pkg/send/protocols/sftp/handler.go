package sftp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"slices"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
	"github.com/pkg/sftp"
)

type listerat []os.FileInfo

func (f listerat) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	var n int
	if offset >= int64(len(f)) {
		return 0, io.EOF
	}
	n = copy(ls, f[offset:])
	if n < len(ls) {
		return n, io.EOF
	}
	return n, nil
}

type handler struct {
	session      *pssh.SSHServerConnSession
	writeHandler utils.CopyFromClientHandler
}

func (f *handler) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Rmdir", "Remove":
		entry := toFileEntry(r)

		if r.Method == "Rmdir" {
			entry.Mode = os.ModeDir
		}

		return f.writeHandler.Delete(f.session, entry)
	case "Mkdir":
		entry := toFileEntry(r)

		entry.Mode = os.ModeDir

		_, err := f.writeHandler.Write(f.session, entry)

		return err
	case "Setstat":
		return nil
	}
	return errors.New("unsupported")
}

func (f *handler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	switch r.Method {
	case "List", "Stat":
		list := r.Method == "List"

		listData, err := f.writeHandler.List(f.session, r.Filepath, list, false)
		if err != nil {
			return nil, err
		}

		// an empty string from minio or exact match from filepath base name is what we want

		if !list {
			listData = slices.DeleteFunc(listData, func(f os.FileInfo) bool {
				return !(f.Name() == "" || f.Name() == filepath.Base(r.Filepath))
			})
		}

		if r.Filepath == "/" {
			listData = slices.DeleteFunc(listData, func(f os.FileInfo) bool {
				return f.Name() == "/"
			})
			listData = slices.Insert(listData, 0, os.FileInfo(&utils.VirtualFile{
				FName:  ".",
				FIsDir: true,
			}))
		}

		return listerat(listData), nil
	}

	return nil, errors.New("unsupported")
}

func toFileEntry(r *sftp.Request) *utils.FileEntry {
	attrs := r.Attributes()
	var size int64 = 0
	var mtime int64 = 0
	var atime int64 = 0
	var mode fs.FileMode
	if attrs != nil {
		mode = attrs.FileMode()
		size = int64(attrs.Size)
		mtime = int64(attrs.Mtime)
		atime = int64(attrs.Atime)
	}

	entry := &utils.FileEntry{
		Filepath: r.Filepath,
		Mode:     mode,
		Size:     size,
		Mtime:    mtime,
		Atime:    atime,
	}
	return entry
}

func (f *handler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	entry := toFileEntry(r)
	entry.Reader = bytes.NewReader([]byte{})

	_, err := f.writeHandler.Write(f.session, entry)
	if err != nil {
		return nil, err
	}

	buf := &buffer{}
	entry.Reader = buf

	return fakeWrite{fileEntry: entry, buf: buf, handler: f}, nil
}

func (f *handler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	if r.Filepath == "/" {
		return nil, os.ErrInvalid
	}

	fileEntry := toFileEntry(r)
	_, reader, err := f.writeHandler.Read(f.session, fileEntry)

	return reader, err
}

type handlererr struct {
	Handler *handler
}

func (f *handlererr) Filecmd(r *sftp.Request) error {
	err := f.Handler.Filecmd(r)
	if err != nil {
		fmt.Fprintln(f.Handler.session.Stderr(), err)
	}
	return err
}
func (f *handlererr) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	result, err := f.Handler.Filelist(r)
	if err != nil {
		fmt.Fprintln(f.Handler.session.Stderr(), err)
	}
	return result, err
}
func (f *handlererr) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	result, err := f.Handler.Filewrite(r)
	if err != nil {
		fmt.Fprintln(f.Handler.session.Stderr(), err)
	}
	return result, err
}
func (f *handlererr) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	result, err := f.Handler.Fileread(r)
	if err != nil {
		fmt.Fprintln(f.Handler.session.Stderr(), err)
	}
	return result, err
}
