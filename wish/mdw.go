package wish

import (
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
)

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
