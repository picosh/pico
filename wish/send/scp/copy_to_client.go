package scp

import (
	"errors"

	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/gliderlabs/ssh"
)

func copyToClient(session ssh.Session, info Info, handler utils.CopyFromClientHandler) error {
	return errors.New("unsupported, use rsync or sftp")
}
