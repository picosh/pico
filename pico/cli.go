package pico

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"

	pipeLogger "github.com/picosh/utils/pipe/log"
)

func getUser(s ssh.Session, dbpool db.DB) (*db.User, error) {
	if s.PublicKey() == nil {
		return nil, fmt.Errorf("key not found")
	}

	key := utils.KeyForKeyText(s.PublicKey())

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
	User       *db.User
	SshSession ssh.Session
	Session    utils.CmdSession
	Log        *slog.Logger
	Dbpool     db.DB
	Write      bool
}

func (c *Cmd) output(out string) {
	_, _ = c.Session.Write([]byte(out + "\r\n"))
}

func (c *Cmd) help() {
	helpStr := "Commands: [help, pico+]\n"
	c.output(helpStr)
}

func (c *Cmd) logs(ctx context.Context) error {
	conn := shared.NewPicoPipeClient()
	stdoutPipe, err := pipeLogger.ReadLogs(ctx, c.Log, conn)

	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 32*1024), 32*1024)
	for scanner.Scan() {
		line := scanner.Text()
		parsedData := map[string]any{}

		err := json.Unmarshal([]byte(line), &parsedData)
		if err != nil {
			c.Log.Error("json unmarshal", "err", err)
			continue
		}

		user := utils.AnyToStr(parsedData, "user")
		userId := utils.AnyToStr(parsedData, "userId")
		if user == c.User.Name || userId == c.User.ID {
			wish.Println(c.SshSession, line)
		}
	}
	return scanner.Err()
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
				wish.Errorf(sesh, "detected ssh command: %s\n", args)
				s := fmt.Errorf("error: you need to create an account before using the remote cli: %w", err)
				wish.Fatalln(sesh, s)
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

				ff, err := dbpool.FindFeatureForUser(user.ID, "plus")
				if err != nil {
					handler.Logger.Error("Unable to find plus feature flag", "err", err, "user", user, "command", args)
					ff, err = dbpool.FindFeatureForUser(user.ID, "bouncer")
					if err != nil {
						handler.Logger.Error("Unable to find bouncer feature flag", "err", err, "user", user, "command", args)
						wish.Fatalln(sesh, "Unable to find plus or bouncer feature flag")
						return
					}
				}

				if ff == nil {
					wish.Fatalln(sesh, "Unable to find plus or bouncer feature flag")
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
				Session:    sesh,
				SshSession: sesh,
				User:       user,
				Log:        log,
				Dbpool:     dbpool,
				Write:      false,
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				if cmd == "help" {
					opts.help()
					return
				} else if cmd == "logs" {
					err = opts.logs(sesh.Context())
					if err != nil {
						wish.Fatalln(sesh, err)
					}
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
