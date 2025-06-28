package pico

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils"

	pipeLogger "github.com/picosh/utils/pipe/log"
)

func getUser(s *pssh.SSHServerConnSession, dbpool db.DB) (*db.User, error) {
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
	SshSession *pssh.SSHServerConnSession
	Session    utils.CmdSession
	Log        *slog.Logger
	Dbpool     db.DB
	Write      bool
}

func (c *Cmd) output(out string) {
	_, _ = c.Session.Write([]byte(out + "\r\n"))
}

func (c *Cmd) help() {
	helpStr := "Commands: [help, user, logs, chat]\n"
	helpStr += "help - this message\n"
	helpStr += "user - display user information (returns name, id, account created, pico+ expiration)\n"
	helpStr += "logs - stream user logs\n"
	helpStr += "chat - IRC chat (must enable pty with `-t` to the SSH command)\n"
	helpStr += "not-found - return all status 404 requests for a host (hostname.com [year|month])\n"
	c.output(helpStr)
}

func (c *Cmd) user() {
	plus := ""
	ff, _ := c.Dbpool.FindFeature(c.User.ID, "plus")
	if ff != nil {
		plus = ff.ExpiresAt.Format(time.RFC3339)
	}
	helpStr := fmt.Sprintf(
		"%s\n%s\n%s\n%s",
		c.User.Name,
		c.User.ID,
		c.User.CreatedAt.Format(time.RFC3339),
		plus,
	)
	c.output(helpStr)
}

func (c *Cmd) notFound(host, interval string) error {
	origin := utils.StartOfYear()
	if interval == "month" {
		origin = utils.StartOfMonth()
	}
	c.output(fmt.Sprintf("starting from: %s\n", origin.Format(time.RFC3339)))
	urls, err := c.Dbpool.VisitUrlNotFound(&db.SummaryOpts{
		Host:   host,
		UserID: c.User.ID,
		Limit:  100,
		Origin: origin,
	})
	if err != nil {
		return err
	}
	for _, url := range urls {
		c.output(fmt.Sprintf("%d %s", url.Count, url.Url))
	}
	return nil
}

func (c *Cmd) logs(ctx context.Context) error {
	conn := shared.NewPicoPipeClient()
	stdoutPipe, err := pipeLogger.ReadLogs(ctx, c.Log, conn)

	if err != nil {
		return err
	}

	logChan := make(chan string)
	defer close(logChan)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case log, ok := <-logChan:
				if log == "" {
					continue
				}
				if !ok {
					return
				}
				_, _ = fmt.Fprintln(c.SshSession, log)
			}
		}
	}()

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 32*1024), 32*1024)
	for scanner.Scan() {
		line := scanner.Text()
		parsedData := map[string]any{}

		err := json.Unmarshal([]byte(line), &parsedData)
		if err != nil {
			c.Log.Error("json unmarshal", "err", err, "line", line, "hidden", true)
			continue
		}

		user := utils.AnyToStr(parsedData, "user")
		userId := utils.AnyToStr(parsedData, "userId")

		hidden := utils.AnyToBool(parsedData, "hidden")

		if !hidden && (user == c.User.Name || userId == c.User.ID) {
			select {
			case logChan <- line:
			case <-ctx.Done():
				return nil
			default:
				c.Log.Error("logChan is full, dropping log", "log", line)
				continue
			}
		}
	}
	return scanner.Err()
}

type CliHandler struct {
	DBPool db.DB
	Logger *slog.Logger
}

func Middleware(handler *CliHandler) pssh.SSHServerMiddleware {
	dbpool := handler.DBPool
	log := handler.Logger

	return func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(sesh *pssh.SSHServerConnSession) error {
			args := sesh.Command()
			if len(args) == 0 {
				return next(sesh)
			}

			user, err := getUser(sesh, dbpool)
			if err != nil {
				_, _ = fmt.Fprintf(sesh.Stderr(), "detected ssh command: %s\n", args)
				s := fmt.Errorf("error: you need to create an account before using the remote cli: %w", err)
				sesh.Fatal(s)
				return s
			}

			if len(args) > 0 && args[0] == "chat" {
				_, _, hasPty := sesh.Pty()
				if !hasPty {
					err := fmt.Errorf(
						"in order to render chat you need to enable PTY with the `ssh -t` flag",
					)

					sesh.Fatal(err)
					return err
				}

				ff, err := dbpool.FindFeature(user.ID, "plus")
				if err != nil {
					handler.Logger.Error("Unable to find plus feature flag", "err", err, "user", user, "command", args)
					ff, err = dbpool.FindFeature(user.ID, "bouncer")
					if err != nil {
						handler.Logger.Error("Unable to find bouncer feature flag", "err", err, "user", user, "command", args)
						sesh.Fatal(err)
						return err
					}
				}

				if ff == nil {
					err = fmt.Errorf("unable to find plus or bouncer feature flag")
					sesh.Fatal(err)
					return err
				}

				pass, err := dbpool.UpsertToken(user.ID, "pico-chat")
				if err != nil {
					sesh.Fatal(err)
					return err
				}
				app, err := shared.NewSenpaiApp(sesh, user.Name, pass)
				if err != nil {
					sesh.Fatal(err)
					return err
				}
				app.Run()
				app.Close()
				return err
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
				switch cmd {
				case "help":
					opts.help()
					return nil
				case "user":
					opts.user()
					return nil
				case "logs":
					err = opts.logs(sesh.Context())
					if err != nil {
						sesh.Fatal(err)
					}
					return nil
				default:
					return next(sesh)
				}
			}

			if cmd == "not-found" {
				if len(args) < 3 {
					sesh.Fatal(fmt.Errorf("must provide host name and interval (`month` or `year`)"))
					return nil
				}
				err = opts.notFound(args[1], args[2])
				if err != nil {
					sesh.Fatal(err)
				}
				return nil
			}

			return next(sesh)
		}
	}
}
