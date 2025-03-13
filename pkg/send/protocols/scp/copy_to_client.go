package scp

import (
	"errors"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
)

func copyToClient(session *pssh.SSHServerConnSession, info Info, handler utils.CopyFromClientHandler) error {
	return errors.New("unsupported, use rsync or sftp")
}
