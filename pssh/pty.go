package pssh

import (
	"fmt"
)

func SessionMessage(sesh *SSHServerConnSession, msg string) {
	_, _ = sesh.Write([]byte(msg + "\r\n"))
}

func DeprecatedNotice() SSHServerMiddleware {
	return func(next SSHServerHandler) SSHServerHandler {
		return func(sesh *SSHServerConnSession) error {
			msg := fmt.Sprintf(
				"%s\n\nRun %s to access pico's TUI",
				"DEPRECATED",
				"ssh pico.sh",
			)
			SessionMessage(sesh, msg)
			return next(sesh)
		}
	}
}

func PtyMdw(mdw SSHServerMiddleware) SSHServerMiddleware {
	return func(next SSHServerHandler) SSHServerHandler {
		return func(sesh *SSHServerConnSession) error {
			_, _, ok := sesh.Pty()
			if !ok {
				return next(sesh)
			}
			return mdw(next)(sesh)
		}
	}
}
