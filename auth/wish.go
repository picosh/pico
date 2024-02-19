package auth

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/cms/util"
)

func findUser(logger *slog.Logger, s ssh.Session, dbpool db.DB) (*db.User, error) {
	var err error
	key, err := util.KeyText(s)
	if err != nil {
		return nil, fmt.Errorf("Key not found")
	}

	username := s.User()
	if username == "new" {
		logger.Info("User requesting to register account")
		return nil, nil
	}

	user, err := dbpool.FindUserForKey(s.User(), key)
	if err != nil {
		logger.Error(err.Error())
		// we only want to throw an error for specific cases
		if errors.Is(err, &db.ErrMultiplePublicKeys{}) {
			return nil, err
		}
		return nil, nil
	}

	if user.Name == "" {
		return nil, fmt.Errorf("Must have username set")
	}

	return user, nil
}

func WishMiddleware(dbpool db.DB, log *slog.Logger, store storage.StorageServe) wish.Middleware {
	return func(sshHandler ssh.Handler) ssh.Handler {
		return func(session ssh.Session) {
			defer func() {
				if r := recover(); r != nil {
					log.Error("Error running auth middleware", "err", r)
					_, _ = session.Stderr().Write([]byte("Error running auth middleware\r\n"))
				}
			}()
			_, _, activePty := session.Pty()
			if activePty {
				_ = session.Exit(0)
				_ = session.Close()
				return
			}

			opts := Cmd{
				Session:  session,
				Username: session.User(),
				Store:    store,
				Log:      log,
				Dbpool:   dbpool,
				Write:    false,
			}

			user, err := findUser(log, session, dbpool)
			if err != nil {
				opts.bail(err)
				return
			}
			// could be nil
			opts.User = user

			args := session.Command()

			cmd := strings.TrimSpace(args[0])
			if cmd == "help" {
				opts.help()
				return
			} else if cmd == "register" {
				username := opts.Username
				if len(args) > 1 && args[1] != "" {
					username = args[1]
				}
				err := opts.register(username)
				opts.bail(err)
				return
			} else {
				e := fmt.Errorf("%s not a valid command", args)
				opts.bail(e)
				return
			}
		}
	}
}
