package auth

import (
	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
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
