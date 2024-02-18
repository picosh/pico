package auth

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/cms/util"
	"github.com/picosh/send/send/utils"
)

func getUser(s ssh.Session, dbpool db.DB) (*db.User, error) {
	var err error
	key, err := util.KeyText(s)
	if err != nil {
		return nil, fmt.Errorf("key not found")
	}

	user, err := dbpool.FindUserForKey(s.User(), key)
	if err != nil {
		return nil, err
	}

	if user.Name == "" {
		return nil, fmt.Errorf("must have username set")
	}

	return user, nil
}

func WishMiddleware(dbpool db.DB, log *slog.Logger, store storage.StorageServe) wish.Middleware {
	return func(sshHandler ssh.Handler) ssh.Handler {
		return func(session ssh.Session) {
			_, _, activePty := session.Pty()
			if activePty {
				_ = session.Exit(0)
				_ = session.Close()
				return
			}

			user, err := getUser(session, dbpool)
			if err != nil {
				utils.ErrorHandler(session, err)
				return
			}

			args := session.Command()

			opts := Cmd{
				Session: session,
				User:    user,
				Store:   store,
				Log:     log,
				Dbpool:  dbpool,
				Write:   false,
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				if cmd == "help" {
					opts.help()
					return
				} else if cmd == "register" {
					err := opts.register()
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
}
