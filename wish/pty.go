package wish

import (
	"fmt"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
)

func SessionMessage(sesh ssh.Session, msg string) {
	_, _ = sesh.Write([]byte(msg + "\r\n"))
}

func DeprecatedNotice() wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {
			msg := fmt.Sprintf(
				"%s\n\nRun %s to access pico's TUI",
				"DEPRECATED",
				"ssh pico.sh",
			)
			SessionMessage(sesh, msg)
			next(sesh)
		}
	}
}

func PtyMdw(mdw wish.Middleware) wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {
			_, _, ok := sesh.Pty()
			if !ok {
				next(sesh)
				return
			}
			mdw(next)(sesh)
		}
	}
}
