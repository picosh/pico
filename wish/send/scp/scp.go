package scp

import (
	"fmt"

	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
)

func Middleware(writeHandler utils.CopyFromClientHandler) wish.Middleware {
	return func(sshHandler ssh.Handler) ssh.Handler {
		return func(session ssh.Session) {
			cmd := session.Command()
			if len(cmd) == 0 || cmd[0] != "scp" {
				sshHandler(session)
				return
			}

			info := GetInfo(cmd)
			if !info.Ok {
				sshHandler(session)
				return
			}

			if info.Recursive {
				err := fmt.Errorf("recursive not supported")
				utils.ErrorHandler(session, err)
				return
			}

			err := writeHandler.Validate(session)
			if err != nil {
				utils.ErrorHandler(session, err)
				return
			}

			switch info.Op {
			case OpCopyToClient:
				err = fmt.Errorf("copying from server to client not supported")
			case OpCopyFromClient:
				if writeHandler == nil {
					err = fmt.Errorf("no handler provided for scp -t")
					break
				}
				err = copyFromClient(session, info, writeHandler)
			}
			if err != nil {
				utils.ErrorHandler(session, err)
				return
			}

			sshHandler(session)
		}
	}
}

// Op defines which kind of SCP Operation is going on.
type Op byte

const (
	// OpCopyToClient is when a file is being copied from the server to the client.
	OpCopyToClient Op = 'f'

	// OpCopyFromClient is when a file is being copied from the client into the server.
	OpCopyFromClient Op = 't'
)

// Info provides some information about the current SCP Operation.
type Info struct {
	// Ok is true if the current session is a SCP.
	Ok bool

	// Recursice is true if its a recursive SCP.
	Recursive bool

	// Path is the server path of the scp operation.
	Path string

	// Op is the SCP operation kind.
	Op Op
}

func GetInfo(cmd []string) Info {
	info := Info{}
	if len(cmd) == 0 || cmd[0] != "scp" {
		return info
	}

	for i, p := range cmd {
		switch p {
		case "-r":
			info.Recursive = true
		case "-f":
			info.Op = OpCopyToClient
			info.Path = cmd[i+1]
		case "-t":
			info.Op = OpCopyFromClient
			info.Path = cmd[i+1]
		}
	}

	info.Ok = true
	return info
}
