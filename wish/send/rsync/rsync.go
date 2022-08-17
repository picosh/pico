package rsync

import (
	"fmt"
	"io"
	"io/fs"
	"log"
	"path"

	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/antoniomika/go-rsync-receiver/rsyncreceiver"
	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
)

type handler struct {
	session      ssh.Session
	writeHandler utils.CopyFromClientHandler
}

func (h *handler) Put(fileName string, content io.Reader, fileSize int64, mTime int64, aTime int64) (written int64, err error) {
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
			err := writeHandler.Validate(session)
			if err != nil {
				utils.ErrorHandler(session, err)
				return
			}

			fileHandler := &handler{
				session:      session,
				writeHandler: writeHandler,
			}

			if _, err := rsyncreceiver.ClientRun(nil, session, fileHandler, true); err != nil {
				log.Println("error running rsync:", err)
			}
		}
	}
}
