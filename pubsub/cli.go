package pubsub

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/google/uuid"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/tui/common"
	psub "github.com/picosh/pubsub"
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
	Styles  common.Styles
}

func (c *Cmd) output(out string) {
	_, _ = c.Session.Write([]byte(out + "\r\n"))
}

func (c *Cmd) error(err error) {
	_, _ = fmt.Fprint(c.Session.Stderr(), err, "\r\n")
	_ = c.Session.Exit(1)
	_ = c.Session.Close()
}

func (c *Cmd) bail(err error) {
	if err == nil {
		return
	}
	c.Log.Error(err.Error())
	c.error(err)
}

func (c *Cmd) help() {
	helpStr := "Commands: [pub, sub, ls]\n"
	c.output(helpStr)
}

func (c *Cmd) ls() error {
	helpStr := "TODO\n"
	c.output(helpStr)
	return nil
}

type CliHandler struct {
	DBPool      db.DB
	Logger      *slog.Logger
	Storage     storage.StorageServe
	RegistryUrl string
	PubSub      *psub.Cfg
}

func WishMiddleware(handler *CliHandler) wish.Middleware {
	dbpool := handler.DBPool
	log := handler.Logger
	pubsub := handler.PubSub

	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {
			user, err := getUser(sesh, dbpool)
			if err != nil {
				utils.ErrorHandler(sesh, err)
				return
			}

			args := sesh.Command()

			opts := Cmd{
				Session: sesh,
				User:    user,
				Log:     log,
				Dbpool:  dbpool,
			}

			if len(args) == 0 {
				opts.help()
				next(sesh)
				return
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				if cmd == "help" {
					opts.help()
				} else if cmd == "ls" {
					err := opts.ls()
					opts.bail(err)
				}
				next(sesh)
				return
			}

			repoName := strings.TrimSpace(args[1])
			cmdArgs := args[2:]
			log.Info(
				"imgs middleware detected command",
				"args", args,
				"cmd", cmd,
				"repoName", repoName,
				"cmdArgs", cmdArgs,
			)

			if cmd == "pub" {
				err = pubsub.PubSub.Pub(&psub.Msg{
					Name:   fmt.Sprintf("%s@%s", user.Name, repoName),
					Reader: sesh,
				})
				if err != nil {
					wish.Errorln(sesh, err)
				}
			} else if cmd == "sub" {
				err = pubsub.PubSub.Sub(&psub.Subscriber{
					ID:      uuid.NewString(),
					Name:    fmt.Sprintf("%s@%s", user.Name, repoName),
					Session: sesh,
					Chan:    make(chan error),
				})
				if err != nil {
					wish.Errorln(sesh, err)
				}
			}

			next(sesh)
		}
	}
}
