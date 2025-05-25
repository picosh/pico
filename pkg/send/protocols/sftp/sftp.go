package sftp

import (
	"errors"
	"fmt"
	"io"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
	"github.com/pkg/sftp"
)

// func SSHOption(writeHandler utils.CopyFromClientHandler) ssh.Option {
// 	return func(server *ssh.Server) error {
// 		if server.SubsystemHandlers == nil {
// 			server.SubsystemHandlers = map[string]ssh.SubsystemHandler{}
// 		}

// 		server.SubsystemHandlers["sftp"] = SubsystemHandler(writeHandler)
// 		return nil
// 	}
// }

func Middleware(writeHandler utils.CopyFromClientHandler) pssh.SSHServerMiddleware {
	return func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(session *pssh.SSHServerConnSession) error {
			logger := writeHandler.GetLogger(session).With(
				"sftp", true,
			)

			defer func() {
				if r := recover(); r != nil {
					logger.Error("error running sftp middleware", "err", r)
					_, _ = fmt.Fprintln(session, "error running sftp middleware, check the flags you are using")
				}
			}()

			err := writeHandler.Validate(session)
			if err != nil {
				_, _ = fmt.Fprintln(session.Stderr(), err)
				return err
			}

			handler := &handlererr{
				Handler: &handler{
					session:      session,
					writeHandler: writeHandler,
				},
			}

			handlers := sftp.Handlers{
				FilePut:  handler,
				FileList: handler,
				FileGet:  handler,
				FileCmd:  handler,
			}

			requestServer := sftp.NewRequestServer(session, handlers)

			err = requestServer.Serve()
			if err != nil && !errors.Is(err, io.EOF) {
				_, _ = fmt.Fprintln(session.Stderr(), err)
				logger.Error("Error serving sftp subsystem", "err", err)
			}

			return err
		}
	}
}
