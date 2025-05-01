package pssh

import (
	"log/slog"
	"time"

	"github.com/picosh/pico/pkg/db"
)

type ctxLoggerKey struct{}
type ctxUserKey struct{}

type FindUserInterface interface {
	FindUser(string) (*db.User, error)
	FindUserByPubkey(string) (*db.User, error)
}

type GetLoggerInterface interface {
	GetLogger(s *SSHServerConnSession) *slog.Logger
}

func LogMiddleware(getLogger GetLoggerInterface, database FindUserInterface) SSHServerMiddleware {
	return func(sshHandler SSHServerHandler) SSHServerHandler {
		return func(s *SSHServerConnSession) error {
			ct := time.Now()

			logger := GetLogger(s)
			if logger == slog.Default() || logger == s.Logger {
				logger = getLogger.GetLogger(s)

				user := GetUser(s)
				if user == nil {
					_, impersonated := s.Permissions().Extensions["imp_id"]

					var user *db.User
					var err error
					var found bool

					if !impersonated {
						pubKey, ok := s.Permissions().Extensions["pubkey"]
						if ok {
							user, err = database.FindUserByPubkey(pubKey)
							found = true
						}
					} else {
						userID, ok := s.Permissions().Extensions["user_id"]
						if ok {
							user, err = database.FindUser(userID)
							found = true
						}
					}

					if found {
						if err == nil && user != nil {
							logger = logger.With(
								"user", user.Name,
								"userId", user.ID,
								"ip", s.RemoteAddr().String(),
							)

							SetUser(s, user)
						} else {
							logger.Error("`user` not set in permissions", "err", err)
						}
					}
				}

				SetLogger(s, logger)
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

	logger = s.Logger

	if v, ok := s.Context().Value(ctxLoggerKey{}).(*slog.Logger); ok {
		return v
	}

	return logger
}

func SetLogger(s *SSHServerConnSession, logger *slog.Logger) {
	if s == nil {
		return
	}

	s.SetValue(ctxLoggerKey{}, logger)
}

func GetUser(s *SSHServerConnSession) *db.User {
	if v, ok := s.Context().Value(ctxUserKey{}).(*db.User); ok {
		return v
	}

	return nil
}

func SetUser(s *SSHServerConnSession, user *db.User) {
	if s == nil {
		return
	}

	s.SetValue(ctxUserKey{}, user)
}
