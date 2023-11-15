package auth

import (
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/wish/send/utils"
)

func Middleware(writeHandler utils.CopyFromClientHandler) wish.Middleware {
	return func(sshHandler ssh.Handler) ssh.Handler {
		return func(session ssh.Session) {
			defer func() {
				if r := recover(); r != nil {
					writeHandler.GetLogger().Error("error running auth middleware: ", r)
					_, _ = session.Stderr().Write([]byte("error running auth middleware\r\n"))
				}
			}()

			err := writeHandler.Validate(session)
			if err != nil {
				utils.ErrorHandler(session, err)
				return
			}

			sshHandler(session)
		}
	}
}
