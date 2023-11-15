package scp

import (
	"fmt"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/wish/send/utils"
)

func Middleware(writeHandler utils.CopyFromClientHandler) wish.Middleware {
	return func(sshHandler ssh.Handler) ssh.Handler {
		return func(session ssh.Session) {
			defer func() {
				if r := recover(); r != nil {
					writeHandler.GetLogger().Error("error running scp middleware: ", r)
					_, _ = session.Stderr().Write([]byte("error running scp middleware, check the flags you are using\r\n"))
				}
			}()

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

			var err error

			switch info.Op {
			case OpCopyToClient:
				if writeHandler == nil {
					err = fmt.Errorf("no handler provided for scp -t")
					break
				}
				err = copyToClient(session, info, writeHandler)
			case OpCopyFromClient:
				if writeHandler == nil {
					err = fmt.Errorf("no handler provided for scp -t")
					break
				}
				err = copyFromClient(session, info, writeHandler)
			}
			if err != nil {
				utils.ErrorHandler(session, err)
			}
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
