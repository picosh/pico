package list

import (
	"sort"
	"strings"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
)

func Middleware(writeHandler utils.CopyFromClientHandler) pssh.SSHServerMiddleware {
	return func(sshHandler pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(session *pssh.SSHServerConnSession) error {
			cmd := session.Command()
			if !(len(cmd) > 1 && cmd[0] == "command" && cmd[1] == "ls") {
				return sshHandler(session)
			}

			fileList, err := writeHandler.List(session, "/", true, false)
			if err != nil {
				utils.ErrorHandler(session, err)
				return err
			}

			var data []string
			for _, file := range fileList {
				name := strings.ReplaceAll(file.Name(), "/", "")
				if file.IsDir() {
					name += "/"
				}

				data = append(data, name)
			}

			sort.Strings(data)

			_, err = session.Write([]byte(strings.Join(data, "\r\n")))
			if err != nil {
				utils.ErrorHandler(session, err)
			}
			return err
		}
	}
}
