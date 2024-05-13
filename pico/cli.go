package pico

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/plus"
	"github.com/picosh/send/send/utils"
)

func getUser(s ssh.Session, dbpool db.DB) (*db.User, error) {
	var err error
	key, err := shared.KeyText(s)
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

type Cmd struct {
	User    *db.User
	Session shared.CmdSession
	Log     *slog.Logger
	Dbpool  db.DB
	Write   bool
	Styles  common.Styles
}

func (c *Cmd) output(out string) {
	_, _ = c.Session.Write([]byte(out + "\r\n"))
}

func (c *Cmd) help() {
	helpStr := "Commands: [help, pico+]\n"
	c.output(helpStr)
}

func (c *Cmd) plus() {
	view := plus.PlusView(c.User.Name)
	c.output(view)
}

type CliHandler struct {
	DBPool db.DB
	Logger *slog.Logger
}

func WishMiddleware(handler *CliHandler) wish.Middleware {
	dbpool := handler.DBPool
	log := handler.Logger

	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {
			args := sesh.Command()
			if len(args) == 0 {
				next(sesh)
				return
			}

			user, err := getUser(sesh, dbpool)
			if err != nil {
				utils.ErrorHandler(sesh, err)
				return
			}

			if len(args) > 0 && args[0] == "chat" {
				_, _, hasPty := sesh.Pty()
				if !hasPty {
					wish.Fatalln(
						sesh,
						"In order to render chat you need to enable PTY with the `ssh -t` flag",
					)
					return
				}

				pass, err := dbpool.UpsertToken(user.ID, "pico-chat")
				if err != nil {
					wish.Fatalln(sesh, err)
					return
				}
				app, err := shared.NewSenpaiApp(sesh, user.Name, pass)
				if err != nil {
					wish.Fatalln(sesh, err)
					return
				}
				app.Run()
				app.Close()
				return
			}

			opts := Cmd{
				Session: sesh,
				User:    user,
				Log:     log,
				Dbpool:  dbpool,
				Write:   false,
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				if cmd == "help" {
					opts.help()
					return
				} else if cmd == "pico+" {
					opts.plus()
					return
				} else {
					next(sesh)
					return
				}
			}

			next(sesh)
		}
	}
}
