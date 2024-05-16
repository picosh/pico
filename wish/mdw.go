package wish

import (
	"fmt"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/picosh/pico/tui/common"
)

func SessionMessage(sesh ssh.Session, msg string) {
	_, _ = sesh.Write([]byte(msg + "\r\n"))
}

func DeprecatedNotice() wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {

			renderer := bm.MakeRenderer(sesh)
			styles := common.DefaultStyles(renderer)

			msg := fmt.Sprintf(
				"%s\n\nRun %s to access pico's TUI",
				styles.Logo.Render("DEPRECATED"),
				styles.Code.Render("ssh pico.sh"),
			)
			SessionMessage(sesh, styles.RoundedBorder.Render(msg))
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
