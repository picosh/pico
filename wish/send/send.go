package send

import (
	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
	"github.com/picosh/pico/wish/pipe"
	"github.com/picosh/pico/wish/send/auth"
	"github.com/picosh/pico/wish/send/rsync"
	"github.com/picosh/pico/wish/send/scp"
	"github.com/picosh/pico/wish/send/sftp"
	"github.com/picosh/pico/wish/send/utils"
)

func Middleware(writeHandler utils.CopyFromClientHandler) ssh.Option {
	return func(server *ssh.Server) error {
		err := wish.WithMiddleware(pipe.Middleware(writeHandler, ""), scp.Middleware(writeHandler), rsync.Middleware(writeHandler), auth.Middleware(writeHandler))(server)
		if err != nil {
			return err
		}

		return sftp.SSHOption(writeHandler)(server)
	}
}
