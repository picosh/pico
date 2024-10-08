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
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/notifications"
	"github.com/picosh/pico/tui/plus"
	"github.com/picosh/utils"

	pipeLogger "github.com/picosh/pubsub/log"
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
	Styles     common.Styles
}

func (c *Cmd) output(out string) {
	_, _ = c.Session.Write([]byte(out + "\r\n"))
}

func (c *Cmd) help() {
	helpStr := "Commands: [help, pico+]\n"
	c.output(helpStr)
}

func (c *Cmd) plus() {
	view := plus.PlusView(c.User.Name, 80)
	c.output(view)
}

func (c *Cmd) notifications() error {
	md := notifications.NotificationsView(c.Dbpool, c.User.ID, 80)
	c.output(md)
	return nil
}

func (c *Cmd) logs(ctx context.Context) error {
	stdoutPipe, err := pipeLogger.ConnectToLogs(ctx, &pipeLogger.PubSubConnectionInfo{
		RemoteHost:     utils.GetEnv("PICO_PIPE_ENDPOINT", "pipe.pico.sh:22"),
		KeyLocation:    utils.GetEnv("PICO_PIPE_KEY", "ssh_data/term_info_ed25519"),
		KeyPassphrase:  utils.GetEnv("PICO_PIPE_PASSPHRASE", ""),
		RemoteHostname: utils.GetEnv("PICO_PIPE_REMOTE_HOST", "pipe.pico.sh"),
		RemoteUser:     utils.GetEnv("PICO_PIPE_USER", "pico"),
	})

	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdoutPipe)
	for scanner.Scan() {
		line := scanner.Text()
		parsedData := map[string]any{}

		err := json.Unmarshal([]byte(line), &parsedData)
		if err != nil {
			c.Log.Error("json unmarshal", "err", err)
			continue
		}

		user := utils.AnyToStr(parsedData, "user")
		if user == c.User.Name {
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
				} else if cmd == "pico+" {
					opts.plus()
					return
				} else if cmd == "notifications" {
					err := opts.notifications()
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
