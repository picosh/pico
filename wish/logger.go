package wish

import (
	"log/slog"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
)

type ctxLoggerKey struct{}
type ctxUserKey struct{}

type FindUserInterface interface {
	FindUserByPubkey(string) (*db.User, error)
}

func LogMiddleware(defaultLogger *slog.Logger, db FindUserInterface) wish.Middleware {
	return func(sh ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			ct := time.Now()

			logger := GetLogger(s)
			if logger == slog.Default() {
				logger = defaultLogger

				user := GetUser(s)
				if user == nil {
					user, err := db.FindUserByPubkey(s.Permissions().Extensions["pubkey"])
					if err == nil && user != nil {
						logger = shared.LoggerWithUser(logger, user).With(
							"ip", s.RemoteAddr(),
						)
						s.Context().SetValue(ctxUserKey{}, user)
					}
				}

				s.Context().SetValue(ctxLoggerKey{}, logger)
			}

			pty, _, ok := s.Pty()

			logger.Info(
				"connect",
				"sshUser", s.User(),
				"pty", ok,
				"term", pty.Term,
				"windowWidth", pty.Window.Width,
				"windowHeight", pty.Window.Height,
			)

			sh(s)

			logger.Info(
				"disconnect",
				"sshUser", s.User(),
				"pty", ok,
				"term", pty.Term,
				"windowWidth", pty.Window.Width,
				"windowHeight", pty.Window.Height,
				"duration", time.Since(ct),
			)
		}
	}
}

func GetLogger(s ssh.Session) *slog.Logger {
	logger := slog.Default()
	if s == nil {
		return logger
	}

	if v, ok := s.Context().Value(ctxLoggerKey{}).(*slog.Logger); ok {
		return v
	}

	return logger
}

func GetUser(s ssh.Session) *db.User {
	if v, ok := s.Context().Value(ctxUserKey{}).(*db.User); ok {
		return v
	}

	return nil
}
