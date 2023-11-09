package scp

import (
	"errors"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/wish/send/utils"
)

func copyToClient(session ssh.Session, info Info, handler utils.CopyFromClientHandler) error {
	return errors.New("unsupported, use rsync or sftp")
}
