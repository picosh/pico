package pgs

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db"
	uploadassets "github.com/picosh/pico/filehandlers/assets"
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

func WishMiddleware(handler *uploadassets.UploadAssetHandler) wish.Middleware {
	dbpool := handler.DBPool
	log := handler.Cfg.Logger
	cfg := handler.Cfg
	store := handler.Storage

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
				} else if cmd == "stats" {
					err := opts.stats(cfg.MaxSize)
					opts.bail(err)
					return
				} else if cmd == "ls" {
					err := opts.ls()
					opts.bail(err)
					return
				} else {
					e := fmt.Errorf("%s not a valid command", args)
					opts.bail(e)
					return
				}
			}

			log.Infof("pgs middleware detected command: %s", args)
			projectName := strings.TrimSpace(args[1])

			if projectName == "--write" {
				utils.ErrorHandler(session, fmt.Errorf("`--write` should be placed at end of command"))
				return
			}

			if cmd == "link" {
				if len(args) < 3 {
					utils.ErrorHandler(session, fmt.Errorf("must supply link command like: `projectA link projectB`"))
					return
				}
				linkTo := strings.TrimSpace(args[2])
				if len(args) >= 4 && strings.TrimSpace(args[3]) == "--write" {
					opts.Write = true
				}

				err := opts.link(projectName, linkTo)
				opts.notice()
				if err != nil {
					opts.bail(err)
				}
				return
			}

			if len(args) >= 3 && strings.TrimSpace(args[2]) == "--write" {
				opts.Write = true
			}

			if cmd == "unlink" {
				err := opts.unlink(projectName)
				opts.notice()
				opts.bail(err)
				return
			} else if cmd == "depends" {
				err := opts.depends(projectName)
				opts.bail(err)
				return
			} else if cmd == "retain" {
				err := opts.prune(projectName, 3)
				opts.notice()
				opts.bail(err)
				return
			} else if cmd == "prune" {
				err := opts.prune(projectName, 0)
				opts.notice()
				opts.bail(err)
				return
			} else if cmd == "rm" {
				err := opts.rm(projectName)
				opts.notice()
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
