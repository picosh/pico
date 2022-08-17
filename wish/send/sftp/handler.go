package sftp

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path"

	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/gliderlabs/ssh"
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
	rootFile     *tempfile
}

func (f *handler) Filecmd(r *sftp.Request) error {
	return errors.New("unsupported")
}

func (f *handler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	switch r.Method {
	case "List":
		fallthrough
	case "Stat":
		if r.Filepath == "/" {
			return listerat{f.rootFile}, nil
		}
	}

	return nil, errors.New("unsupported")
}

func (f *handler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	fileEntry := &utils.FileEntry{
		Name:     path.Base(r.Filepath),
		Filepath: r.Filepath,
		Mode:     r.Attributes().FileMode(),
		Size:     int64(r.Attributes().Size),
		Mtime:    int64(r.Attributes().Mtime),
		Atime:    int64(r.Attributes().Atime),
	}

	buf := bytes.NewBuffer([]byte{})
	fileEntry.Reader = buf

	return fakeWrite{fileEntry: fileEntry, buf: buf, handler: f}, nil
}

func (f *handler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	if r.Filepath == "/" {
		return nil, os.ErrInvalid
	}

	return nil, errors.New("unsupported")
}
