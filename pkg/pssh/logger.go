package pssh

import (
	"log/slog"
	"time"

	"github.com/picosh/pico/pkg/db"
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

			width, height := 0, 0
			term := ""
			if pty != nil {
				term = pty.Term
				width = pty.Window.Width
				height = pty.Window.Height
			}

			logger.Info(
				"connect",
				"sshUser", s.User(),
				"pty", ok,
				"term", term,
				"windowWidth", width,
				"windowHeight", height,
			)

			err := sshHandler(s)
			if err != nil {
				logger.Error("error", "err", err)
			}

			if pty != nil {
				term = pty.Term
				width = pty.Window.Width
				height = pty.Window.Height
			}

			logger.Info(
				"disconnect",
				"sshUser", s.User(),
				"pty", ok,
				"term", term,
				"windowWidth", width,
				"windowHeight", height,
				"duration", time.Since(ct),
				"err", err,
			)

			return err
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
