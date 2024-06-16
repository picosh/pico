package prose

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui/common"
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

func (c *Cmd) statsByPost(postSlug string) error {
	post, err := c.Dbpool.FindPostWithSlug(postSlug, c.User.ID, "prose")
	if err != nil {
		return errors.Join(err, fmt.Errorf("post (%s) does not exit", postSlug))
	}

	opts := &db.SummaryOpts{
		FkID:     post.ID,
		By:       "post_id",
		Interval: "day",
		Origin:   shared.StartOfMonth(),
	}

	summary, err := c.Dbpool.VisitSummary(opts)
	if err != nil {
		return err
	}

	c.output("Top URLs")
	topUrlsTbl := common.VisitUrlsTbl(summary.TopUrls)
	c.output(topUrlsTbl.Width(c.Width).String())

	c.output("Top Referers")
	topRefsTbl := common.VisitUrlsTbl(summary.TopReferers)
	c.output(topRefsTbl.Width(c.Width).String())

	uniqueTbl := common.UniqueVisitorsTbl(summary.Intervals)
	c.output("Unique Visitors this Month")
	c.output(uniqueTbl.Width(c.Width).String())

	return nil
}

func (c *Cmd) stats() error {
	opts := &db.SummaryOpts{
		FkID:     c.User.ID,
		By:       "user_id",
		Interval: "day",
		Origin:   shared.StartOfMonth(),
		Where:    "AND (post_id IS NOT NULL OR (post_id IS NULL AND project_id IS NULL))",
	}

	summary, err := c.Dbpool.VisitSummary(opts)
	if err != nil {
		return err
	}

	c.output("Top URLs")
	topUrlsTbl := common.VisitUrlsTbl(summary.TopUrls)
	c.output(topUrlsTbl.Width(c.Width).String())

	c.output("Top Referers")
	topRefsTbl := common.VisitUrlsTbl(summary.TopReferers)
	c.output(topRefsTbl.Width(c.Width).String())

	uniqueTbl := common.UniqueVisitorsTbl(summary.Intervals)
	c.output("Unique Visitors this Month")
	c.output(uniqueTbl.Width(c.Width).String())

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
					opts.stats()
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
