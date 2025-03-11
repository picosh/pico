package pssh

import (
	"log/slog"
	"time"

	"github.com/picosh/pico/db"
)

type ctxLoggerKey struct{}
type ctxUserKey struct{}

type FindUserInterface interface {
	FindUserByPubkey(string) (*db.User, error)
}

type GetLoggerInterface interface {
	GetLogger(s *SSHServerConnSession) *slog.Logger
}

func LogMiddleware(getLogger GetLoggerInterface, db FindUserInterface) SSHServerMiddleware {
	return func(sshHandler SSHServerHandler) SSHServerHandler {
		return func(s *SSHServerConnSession) error {
			ct := time.Now()

			logger := GetLogger(s)
			if logger == slog.Default() {
				logger = getLogger.GetLogger(s)

				user := GetUser(s)
				if user == nil {
					user, err := db.FindUserByPubkey(s.Permissions().Extensions["pubkey"])
					if err == nil && user != nil {
						logger = logger.With(
							"user", user.Name,
							"userId", user.ID,
							"ip", s.RemoteAddr().String(),
						)
						s.SetValue(ctxUserKey{}, user)
					}
				}

				s.SetValue(ctxLoggerKey{}, logger)
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

			sshHandler(s)

			logger.Info(
				"disconnect",
				"sshUser", s.User(),
				"pty", ok,
				"term", pty.Term,
				"windowWidth", pty.Window.Width,
				"windowHeight", pty.Window.Height,
				"duration", time.Since(ct),
			)

			return nil
		}
	}
}

func GetLogger(s *SSHServerConnSession) *slog.Logger {
	logger := slog.Default()
	if s == nil {
		return logger
	}

	if v, ok := s.Context().Value(ctxLoggerKey{}).(*slog.Logger); ok {
		return v
	}

	return logger
}

func GetUser(s *SSHServerConnSession) *db.User {
	if v, ok := s.Context().Value(ctxUserKey{}).(*db.User); ok {
		return v
	}

	return nil
}
