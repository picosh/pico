package prose

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/muesli/termenv"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/utils"
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
	User    *db.User
	Session utils.CmdSession
	Log     *slog.Logger
	Dbpool  db.DB
	Styles  common.Styles
	Width   int
	Height  int
}

func (c *Cmd) output(out string) {
	_, _ = c.Session.Write([]byte(out + "\r\n"))
}

func (c *Cmd) help() {
	helpStr := "Commands: [help, stats]\n"
	c.output(helpStr)
}

func (c *Cmd) statsByPost(_ string) error {
	msg := fmt.Sprintf(
		"%s\n\nRun %s to access pico's analytics TUI",
		c.Styles.Logo.Render("DEPRECATED"),
		c.Styles.Code.Render("ssh pico.sh"),
	)
	c.output(c.Styles.RoundedBorder.Render(msg))
	return nil
}

func (c *Cmd) stats() error {
	return c.statsByPost("")
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

			// default width and height when no pty
			width := 80
			height := 24
			pty, _, ok := sesh.Pty()
			if ok {
				width = pty.Window.Width
				height = pty.Window.Height
			}

			user, err := getUser(sesh, dbpool)
			if err != nil {
				wish.Errorln(sesh, err)
				return
			}

			renderer := bm.MakeRenderer(sesh)
			renderer.SetColorProfile(termenv.TrueColor)
			styles := common.DefaultStyles(renderer)

			opts := Cmd{
				Session: sesh,
				User:    user,
				Log:     log,
				Dbpool:  dbpool,
				Styles:  styles,
				Width:   width,
				Height:  height,
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				if cmd == "help" {
					opts.help()
					return
				} else if cmd == "stats" {
					err := opts.stats()
					if err != nil {
						wish.Fatalln(sesh, err)
					}
					return
				} else {
					next(sesh)
					return
				}
			}

			postSlug := strings.TrimSpace(args[1])
			cmdArgs := args[2:]
			log.Info(
				"pgs middleware detected command",
				"args", args,
				"cmd", cmd,
				"post", postSlug,
				"cmdArgs", cmdArgs,
			)

			if cmd == "stats" {
				err := opts.statsByPost(postSlug)
				if err != nil {
					wish.Fatalln(sesh, err)
				}
				return
			} else {
				next(sesh)
				return
			}
		}
	}
}
