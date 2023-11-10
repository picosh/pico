package sftp

import (
	"bytes"
	"errors"
	"io"
	"os"

	"slices"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/wish/send/utils"
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
	session      ssh.Session
	writeHandler utils.CopyFromClientHandler
}

func (f *handler) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Remove":
		entry := toFileEntry(r)
		entry.Reader = bytes.NewReader(nil)

		_, err := f.writeHandler.Write(f.session, entry)

		return err
	case "Setstat", "Mkdir":
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

		if list {
			listData = slices.DeleteFunc(listData, func(f os.FileInfo) bool {
				return f.Name() == "/"
			})
		}

		return listerat(listData), nil
	}

	return nil, errors.New("unsupported")
}

func toFileEntry(r *sftp.Request) *utils.FileEntry {
	entry := &utils.FileEntry{
		Filepath: r.Filepath,
		Mode:     r.Attributes().FileMode(),
		Size:     int64(r.Attributes().Size),
		Mtime:    int64(r.Attributes().Mtime),
		Atime:    int64(r.Attributes().Atime),
	}
	return entry
}

func (f *handler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	entry := toFileEntry(r)
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
