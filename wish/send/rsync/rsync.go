package rsync

import (
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"

	"github.com/picosh/pico/wish/send/utils"
	"github.com/antoniomika/go-rsync-receiver/rsyncreceiver"
	"github.com/antoniomika/go-rsync-receiver/rsyncsender"
	rsyncutils "github.com/antoniomika/go-rsync-receiver/utils"
	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
)

type handler struct {
	session      ssh.Session
	writeHandler utils.CopyFromClientHandler
}

func (h *handler) Skip(file *rsyncutils.ReceiverFile) bool {
	return false
}

func (h *handler) List(path string) ([]os.FileInfo, error) {
	list, err := h.writeHandler.List(h.session, path)
	if err != nil {
		return nil, err
	}

	newList := list
	if list[0].IsDir() {
		newList = list[1:]
	}

	return newList, nil
}

func (h *handler) Read(path string) (os.FileInfo, io.ReaderAt, error) {
	return h.writeHandler.Read(h.session, path)
}

func (h *handler) Put(fileName string, content io.Reader, fileSize int64, mTime int64, aTime int64) (int64, error) {
	cleanName := path.Base(fileName)
	fileEntry := &utils.FileEntry{
		Name:     cleanName,
		Filepath: fmt.Sprintf("/%s", cleanName),
		Mode:     fs.FileMode(0600),
		Size:     fileSize,
		Mtime:    mTime,
		Atime:    aTime,
	}

	fileEntry.Reader = content

	msg, err := h.writeHandler.Write(h.session, fileEntry)
	if err != nil {
		errMsg := fmt.Sprintf("%s\n", err.Error())
		_, err = h.session.Stderr().Write([]byte(errMsg))
	}
	if msg != "" {
		nMsg := fmt.Sprintf("%s\n", msg)
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
			}

			for _, arg := range cmd {
				if arg == "--sender" {
					if err := rsyncsender.ClientRun(nil, session, fileHandler, cmd[len(cmd)-1], true); err != nil {
						log.Println("error running rsync:", err)
					}
					return
				}
			}

			if _, err := rsyncreceiver.ClientRun(nil, session, fileHandler, true); err != nil {
				log.Println("error running rsync:", err)
			}
		}
	}
}
