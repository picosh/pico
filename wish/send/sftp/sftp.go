package sftp

import (
	"errors"
	"io"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/wish/send/utils"
	"github.com/pkg/sftp"
)

func SSHOption(writeHandler utils.CopyFromClientHandler) ssh.Option {
	return func(server *ssh.Server) error {
		if server.SubsystemHandlers == nil {
			server.SubsystemHandlers = map[string]ssh.SubsystemHandler{}
		}

		server.SubsystemHandlers["sftp"] = SubsystemHandler(writeHandler)
		return nil
	}
}

func SubsystemHandler(writeHandler utils.CopyFromClientHandler) ssh.SubsystemHandler {
	return func(session ssh.Session) {
		defer func() {
			if r := recover(); r != nil {
				writeHandler.GetLogger().Error("error running sftp middleware: ", r)
				_, _ = session.Stderr().Write([]byte("error running sftp middleware, check the flags you are using\r\n"))
			}
		}()

		err := writeHandler.Validate(session)
		if err != nil {
			utils.ErrorHandler(session, err)
			return
		}

		handler := &handler{
			session:      session,
			writeHandler: writeHandler,
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
			writeHandler.GetLogger().Error("Error serving sftp subsystem: ", err)
		}
	}
}
