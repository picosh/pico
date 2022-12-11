package auth

import (
	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
	"github.com/picosh/pico/wish/send/utils"
)

func Middleware(writeHandler utils.CopyFromClientHandler) wish.Middleware {
	return func(sshHandler ssh.Handler) ssh.Handler {
		return func(session ssh.Session) {
			err := writeHandler.Validate(session)
			if err != nil {
				utils.ErrorHandler(session, err)
				return
			}

			sshHandler(session)
		}
	}
}
