package auth

import (
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
)

func Middleware(writeHandler utils.CopyFromClientHandler) pssh.SSHServerMiddleware {
	return func(sshHandler pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(session *pssh.SSHServerConnSession) error {
			defer func() {
				if r := recover(); r != nil {
					writeHandler.GetLogger(session).Error("error running auth middleware", "err", r)
				}
			}()

			err := writeHandler.Validate(session)
			if err != nil {
				utils.ErrorHandler(session, err)
				return err
			}

			return sshHandler(session)
		}
	}
}
