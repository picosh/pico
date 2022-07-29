package sftp

import (
	"errors"
	"io"
	"log"

	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/gliderlabs/ssh"
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
		rootFile := &tempfile{
			name:  "/",
			isDir: true,
		}
		handler := &handler{
			session:      session,
			writeHandler: writeHandler,
			rootFile:     rootFile,
		}
		handlers := sftp.Handlers{
			FilePut:  handler,
			FileList: handler,
			FileGet:  handler,
			FileCmd:  handler,
		}

		requestServer := sftp.NewRequestServer(session, handlers)

		err := requestServer.Serve()
		if err != nil && !errors.Is(err, io.EOF) {
			log.Println("Error serving sftp subsystem:", err)
		}
	}
}
