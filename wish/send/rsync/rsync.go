package rsync

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
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
}

func (h *handler) Skip(file *rsyncutils.ReceiverFile) bool {
	log.Printf("SKIP %+v", file)
	return file.FileMode().IsDir()
}

func (h *handler) List(rPath string) ([]fs.FileInfo, error) {
	log.Println("LIST", rPath)
	isDir := false
	if rPath == "." {
		rPath = "/"
		isDir = true
	}

	list, err := h.writeHandler.List(h.session, rPath, isDir, true)
	if err != nil {
		return nil, err
	}

	for _, f := range list {
		log.Printf("first %+v", f)
	}

	var dirs []string

	var newList []fs.FileInfo

	for _, f := range list {
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

	for _, f := range newList {
		log.Printf("%+v", f)
	}

	if len(newList) == 0 {
		return nil, errors.New("no files to process")
	}

	return newList, nil
}

func (h *handler) Read(file *rsyncutils.SenderFile) (os.FileInfo, io.ReaderAt, error) {
	log.Printf("READ %+v %s", file, h.root)

	filePath := file.WPath

	if strings.HasSuffix(h.root, file.WPath) {
		filePath = h.root
	} else if !strings.HasPrefix(filePath, h.root) {
		filePath = path.Join(h.root, file.Path, file.WPath)
	}

	log.Printf("READ %+v %s", file, filePath)

	return h.writeHandler.Read(h.session, &utils.FileEntry{Filepath: filePath})
}

func (h *handler) Put(file *rsyncutils.ReceiverFile) (int64, error) {
	log.Printf("PUT %+v", file)
	fpath := path.Join("/", h.root)
	fileEntry := &utils.FileEntry{
		Filepath: filepath.Join(fpath, file.Name),
		Mode:     fs.FileMode(0600),
		Size:     file.Length,
		Mtime:    file.ModTime.Unix(),
		Atime:    file.ModTime.Unix(),
	}
	log.Printf("%+v", fileEntry)
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
					_, _ = parser.Parse(cmdFlags)

					if err := rsyncsender.ClientRun(opts, session, fileHandler, fileHandler.root, true); err != nil {
						log.Println("error running rsync sender:", err)
					}
					return
				}
			}

			opts, parser := rsyncreceiver.NewGetOpt()
			_, _ = parser.Parse(cmdFlags)

			if _, err := rsyncreceiver.ClientRun(opts, session, fileHandler, true); err != nil {
				log.Println("error running rsync receiver:", err)
			}
		}
	}
}
