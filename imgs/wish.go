package imgs

import (
	"flag"
	"fmt"
	"log/slog"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui/common"
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

func flagSet(cmdName string, sesh ssh.Session) (*flag.FlagSet, *bool) {
	cmd := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	cmd.SetOutput(sesh)
	write := cmd.Bool("write", false, "apply changes")
	return cmd, write
}

func flagCheck(cmd *flag.FlagSet, posArg string, cmdArgs []string) bool {
	_ = cmd.Parse(cmdArgs)

	if posArg == "-h" || posArg == "--help" || posArg == "-help" {
		cmd.Usage()
		return false
	}
	return true
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

func (c *Cmd) notice() {
	if !c.Write {
		c.output("\nNOTICE: changes not commited, use `--write` to save operation")
	}
}

func (c *Cmd) help() {
	helpStr := "Commands: [help, ls, rm]\n"
	helpStr += "NOTICE: *must* append with `--write` for the changes to persist.\n"
	c.output(helpStr)
}

func (c *Cmd) rm(repo string) error {
	return nil
}

func (c *Cmd) ls() error {
	return nil
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
				Write:   false,
			}

			if len(args) == 0 {
				next(sesh)
				return
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				if cmd == "help" {
					opts.help()
					return
				} else if cmd == "ls" {
					err := opts.ls()
					opts.bail(err)
					return
				} else {
					next(sesh)
					return
				}
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

			if cmd == "rm" {
				rmCmd, write := flagSet("rm", sesh)
				if !flagCheck(rmCmd, repoName, cmdArgs) {
					return
				}
				opts.Write = *write

				err := opts.rm(repoName)
				opts.notice()
				opts.bail(err)
				return
			} else {
				next(sesh)
				return
			}
		}
	}
}
