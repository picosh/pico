package pipe

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
)

func Middleware(writeHandler utils.CopyFromClientHandler, ext string) wish.Middleware {
	return func(sshHandler ssh.Handler) ssh.Handler {
		return func(session ssh.Session) {
			_, _, activePty := session.Pty()
			if activePty {
				_ = session.Exit(0)
				_ = session.Close()
				return
			}

			err := writeHandler.Validate(session)
			if err != nil {
				utils.ErrorHandler(session, err)
				return
			}

			name := strings.TrimSpace(strings.Join(session.Command(), " "))
			postTime := time.Now()

			if name == "" {
				name = fmt.Sprintf("%s%s", strconv.Itoa(int(postTime.UnixNano())), ext)
			}

			result, err := writeHandler.Write(session, &utils.FileEntry{
				Name:     name,
				Filepath: name,
				Mode:     fs.FileMode(0777),
				Size:     0,
				Mtime:    postTime.Unix(),
				Atime:    postTime.Unix(),
				Reader:   session,
			})
			if err != nil {
				utils.ErrorHandler(session, err)
				return
			}

			if result != "" {
				_, err = session.Write([]byte(fmt.Sprintf("%s\n", result)))
				if err != nil {
					utils.ErrorHandler(session, err)
				}
				return
			}

			sshHandler(session)
		}
	}
}
