package wish

import (
	"log/slog"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
)

func LogMiddleware(logger *slog.Logger) wish.Middleware {
	return func(sh ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			ct := time.Now()
			pty, _, ok := s.Pty()

			logger.Info(
				"connect",
				"user", s.User(),
				"ip", s.RemoteAddr().String(),
				"pty", ok,
				"term", pty.Term,
				"windowWidth", pty.Window.Width,
				"windowHeight", pty.Window.Height,
			)

			sh(s)

			logger.Info(
				"disconnect",
				"ip", s.RemoteAddr().String(),
				"duration", time.Since(ct),
			)
		}
	}
}
