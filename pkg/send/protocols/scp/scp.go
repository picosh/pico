package scp

import (
	"fmt"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
)

func Middleware(writeHandler utils.CopyFromClientHandler) pssh.SSHServerMiddleware {
	return func(sshHandler pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(session *pssh.SSHServerConnSession) error {
			cmd := session.Command()
			if len(cmd) == 0 || cmd[0] != "scp" {
				return sshHandler(session)
			}

			logger := writeHandler.GetLogger(session).With(
				"scp", true,
				"cmd", cmd,
			)

			defer func() {
				if r := recover(); r != nil {
					logger.Error("error running scp middleware", "err", r)
					_, _ = session.Stderr().Write([]byte("error running scp middleware, check the flags you are using\r\n"))
				}
			}()

			info := GetInfo(cmd)
			if !info.Ok {
				return sshHandler(session)
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

			return err
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
