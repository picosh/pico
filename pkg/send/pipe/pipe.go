package pipe

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
)

func Middleware(writeHandler utils.CopyFromClientHandler, ext string) pssh.SSHServerMiddleware {
	return func(sshHandler pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(session *pssh.SSHServerConnSession) error {
			_, _, activePty := session.Pty()
			if activePty {
				_ = session.Exit(0)
				err := session.Close()
				return err
			}

			cmd := session.Command()

			name := ""
			if len(cmd) > 0 {
				name = strings.TrimSpace(cmd[0])
				if strings.Contains(name, "=") {
					name = ""
				}
			}

			postTime := time.Now()

			if name == "" {
				name = fmt.Sprintf("%s%s", strconv.Itoa(int(postTime.UnixNano())), ext)
			}

			result, err := writeHandler.Write(session, &utils.FileEntry{
				Filepath: name,
				Mode:     fs.FileMode(0777),
				Size:     0,
				Mtime:    postTime.Unix(),
				Atime:    postTime.Unix(),
				Reader:   session,
			})
			if err != nil {
				utils.ErrorHandler(session, err)
				return err
			}

			if result != "" {
				_, err := fmt.Fprintf(session, "%s\r\n", result)
				if err != nil {
					utils.ErrorHandler(session, err)
				}
				return err
			}

			return sshHandler(session)
		}
	}
}
