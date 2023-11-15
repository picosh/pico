package rsync

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/antoniomika/go-rsync-receiver/rsyncreceiver"
	"github.com/antoniomika/go-rsync-receiver/rsyncsender"
	rsyncutils "github.com/antoniomika/go-rsync-receiver/utils"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/wish/send/utils"
)

type handler struct {
	session      ssh.Session
	writeHandler utils.CopyFromClientHandler
	root         string
	recursive    bool
	ignoreTimes  bool
}

func (h *handler) Skip(file *rsyncutils.ReceiverFile) bool {
	if file.FileMode().IsDir() {
		return true
	}

	fI, _, err := h.writeHandler.Read(h.session, &utils.FileEntry{Filepath: path.Join("/", h.root, file.Name)})
	if err == nil && fI.ModTime().Equal(file.ModTime) && file.Length == fI.Size() {
		return true
	}

	return false
}

func (h *handler) List(rPath string) ([]fs.FileInfo, error) {
	isDir := false
	if rPath == "." {
		rPath = "/"
		isDir = true
	}

	list, err := h.writeHandler.List(h.session, rPath, isDir, h.recursive)
	if err != nil {
		return nil, err
	}

	var dirs []string

	var newList []fs.FileInfo

	for _, f := range list {
		if !f.IsDir() && f.Size() == 0 {
			continue
		}

		fname := f.Name()
		if strings.HasPrefix(f.Name(), "/") {
			fname = path.Join(rPath, f.Name())
		}

		if fname == "" && !f.IsDir() {
			fname = path.Base(rPath)
		}

		newFile := &utils.VirtualFile{
			FName:    fname,
			FIsDir:   f.IsDir(),
			FSize:    f.Size(),
			FModTime: f.ModTime(),
			FSys:     f.Sys(),
		}

		newList = append(newList, newFile)

		parts := strings.Split(newFile.Name(), string(os.PathSeparator))
		lastDir := newFile.Name()
		for i := 0; i < len(parts); i++ {
			lastDir, _ = path.Split(lastDir)
			if lastDir == "" {
				continue
			}

			lastDir = lastDir[:len(lastDir)-1]
			dirs = append(dirs, lastDir)
		}
	}

	for _, dir := range dirs {
		newList = append(newList, &utils.VirtualFile{
			FName:  dir,
			FIsDir: true,
		})
	}

	slices.Reverse(newList)

	onlyEmpty := true
	for _, f := range newList {
		if f.Name() != "" {
			onlyEmpty = false
		}
	}

	if len(newList) == 0 || onlyEmpty {
		return nil, errors.New("no files to send, the directory may not exist or could be empty")
	}

	return newList, nil
}

func (h *handler) Read(file *rsyncutils.SenderFile) (os.FileInfo, io.ReaderAt, error) {
	filePath := file.WPath

	if strings.HasSuffix(h.root, file.WPath) {
		filePath = h.root
	} else if !strings.HasPrefix(filePath, h.root) {
		filePath = path.Join(h.root, file.Path, file.WPath)
	}

	return h.writeHandler.Read(h.session, &utils.FileEntry{Filepath: filePath})
}

func (h *handler) Put(file *rsyncutils.ReceiverFile) (int64, error) {
	fileEntry := &utils.FileEntry{
		Filepath: path.Join("/", h.root, file.Name),
		Mode:     fs.FileMode(0600),
		Size:     file.Length,
		Mtime:    file.ModTime.Unix(),
		Atime:    file.ModTime.Unix(),
	}
	fileEntry.Reader = file.Buf

	msg, err := h.writeHandler.Write(h.session, fileEntry)
	if err != nil {
		errMsg := fmt.Sprintf("%s\r\n", err.Error())
		_, err = h.session.Stderr().Write([]byte(errMsg))
	}
	if msg != "" {
		nMsg := fmt.Sprintf("%s\r\n", msg)
		_, err = h.session.Stderr().Write([]byte(nMsg))
	}
	return 0, err
}

func Middleware(writeHandler utils.CopyFromClientHandler) wish.Middleware {
	return func(sshHandler ssh.Handler) ssh.Handler {
		return func(session ssh.Session) {
			defer func() {
				if r := recover(); r != nil {
					writeHandler.GetLogger().Error("error running rsync middleware: ", r)
					_, _ = session.Stderr().Write([]byte("error running rsync middleware, check the flags you are using\r\n"))
				}
			}()

			cmd := session.Command()
			if len(cmd) == 0 || cmd[0] != "rsync" {
				sshHandler(session)
				return
			}

			fileHandler := &handler{
				session:      session,
				writeHandler: writeHandler,
				root:         strings.TrimPrefix(cmd[len(cmd)-1], "/"),
			}

			cmdFlags := session.Command()

			for _, arg := range cmd {
				if arg == "--sender" {
					opts, parser := rsyncsender.NewGetOpt()

					compress := parser.Bool("z", false)

					_, _ = parser.Parse(cmdFlags[1:])

					fileHandler.recursive = opts.Recurse
					fileHandler.ignoreTimes = opts.IgnoreTimes

					if *compress {
						_, _ = session.Stderr().Write([]byte("compression is currently unsupported\r\n"))
						return
					}

					if opts.PreserveUid {
						_, _ = session.Stderr().Write([]byte("uid preservation will not work as we don't retain user information\r\n"))
						return
					}

					if opts.PreserveGid {
						_, _ = session.Stderr().Write([]byte("gid preservation will not work as we don't retain user information\r\n"))
						return
					}

					if err := rsyncsender.ClientRun(opts, session, fileHandler, fileHandler.root, true); err != nil {
						writeHandler.GetLogger().Error("error running rsync sender: ", err)
					}
					return
				}
			}

			opts, parser := rsyncreceiver.NewGetOpt()

			compress := parser.Bool("z", false)

			_, _ = parser.Parse(cmdFlags[1:])

			fileHandler.recursive = opts.Recurse
			fileHandler.ignoreTimes = opts.IgnoreTimes

			if *compress {
				_, _ = session.Stderr().Write([]byte("compression is currently unsupported\r\n"))
				return
			}

			if opts.PreserveUid {
				_, _ = session.Stderr().Write([]byte("uid preservation will not work as we don't retain user information\r\n"))
				return
			}

			if opts.PreserveGid {
				_, _ = session.Stderr().Write([]byte("gid preservation will not work as we don't retain user information\r\n"))
				return
			}

			if _, err := rsyncreceiver.ClientRun(opts, session, fileHandler, true); err != nil {
				writeHandler.GetLogger().Error("error running rsync receiver: ", err)
			}
		}
	}
}
