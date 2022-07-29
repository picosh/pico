package send

import (
	"git.sr.ht/~erock/pico/wish/send/scp"
	"git.sr.ht/~erock/pico/wish/send/sftp"
	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
)

func Middleware(writeHandler utils.CopyFromClientHandler) ssh.Option {
	return func(server *ssh.Server) error {
		err := wish.WithMiddleware(scp.Middleware(writeHandler))(server)
		if err != nil {
			return err
		}

		return sftp.SSHOption(writeHandler)(server)
	}
}
