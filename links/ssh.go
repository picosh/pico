package links

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/promwish"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
	"github.com/urfave/cli"
)

func StartSshServer(cfg *LinksConfig, killCh chan error) {
	logger := cfg.Logger

	ctx := context.Background()
	defer ctx.Done()

	sshAuth := shared.NewSshAuthHandler(cfg.DB, logger)
	s, err := wish.NewServer(
		wish.WithAddress(
			fmt.Sprintf("%s:%s", cfg.SshHost, cfg.SshPort),
		),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(sshAuth.PubkeyAuthHandler),
		wish.WithMiddleware(
			CliMiddleware(cfg),
			promwish.Middleware(fmt.Sprintf("%s:%s", cfg.SshHost, cfg.PromPort), "links-ssh"),
		),
	)
	if err != nil {
		logger.Error(err.Error())
		return
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("starting SSH server on", "host", cfg.SshHost, "port", cfg.SshPort)
	go func() {
		if err = s.ListenAndServe(); err != nil {
			logger.Error("serve", "err", err.Error())
			os.Exit(1)
		}
	}()

	select {
	case <-done:
		logger.Info("stopping ssh server")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer func() { cancel() }()
		if err := s.Shutdown(ctx); err != nil {
			logger.Error("shutdown", "err", err.Error())
			os.Exit(1)
		}
	case <-killCh:
		logger.Info("stopping ssh server")
	}
}

func CliMiddleware(cfg *LinksConfig) wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {
			args := sesh.Command()
			cli := NewCli(sesh, cfg)
			margs := append([]string{"links"}, args...)
			err := cli.Run(margs)
			if err != nil {
				cfg.Logger.Error("error when running cli", "err", err)
				wish.Fatalln(sesh, fmt.Errorf("err: %w", err))
				next(sesh)
				return
			}
		}
	}
}

func NewTabWriter(out io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 0, 1, ' ', tabwriter.TabIndent)
}

func NewCli(sesh ssh.Session, cfg *LinksConfig) *cli.App {
	pubkey := utils.KeyForKeyText(sesh.PublicKey())
	user, err := cfg.DB.FindUserByPubkey(pubkey)
	if err != nil {
		cfg.Logger.Error("find user by pubkey", "err", err)
		return nil
	}

	app := &cli.App{
		Name:        "ssh",
		Description: "Link aggregator for hackers",
		Usage:       "Link aggregator for hackers",
		Writer:      sesh,
		ErrWriter:   sesh,
		ExitErrHandler: func(cCtx *cli.Context, err error) {
			if err != nil {
				wish.Fatalln(sesh, fmt.Errorf("err: %w", err))
			}
		},
		OnUsageError: func(cCtx *cli.Context, err error, isSubcommand bool) error {
			if err != nil {
				wish.Fatalln(sesh, fmt.Errorf("err: %w", err))
			}
			return nil
		},
		Commands: []cli.Command{
			{
				Name:    "show",
				Aliases: []string{"s"},
				Usage:   "Show the link tree",
				Action: func(cCtx *cli.Context) error {
					shortID := cCtx.Args().First()
					path := ""
					if shortID == "" {
						path = "root"
					} else {
						replyLink, err := cfg.DB.FindLinkByShortID(shortID)
						if err != nil {
							return err
						}
						path = replyLink.Path
					}

					links, err := cfg.DB.FindLinks(path, &db.Pager{Num: 1000, Page: 0})
					if err != nil {
						return err
					}

					wish.Printf(sesh, "breadcrumbs: %s\n\n", path)

					orig := strings.Split(path, ".")
					for _, link := range links.Data {
						sp := strings.Split(link.Path, ".")
						ident := len(sp) - len(orig)
						col := strings.Repeat(" ", int(ident))
						wish.Printf(
							sesh, "%s|%d %s %s.%s\n",
							col, link.Votes, link.Username, link.Path, link.ShortID,
						)
						wish.Printf(sesh, "%s|%s\n", col, link.Text)
					}

					return nil
				},
			},
			{
				Name:    "reply",
				Aliases: []string{"r"},
				Usage:   "Submit a new link",
				Action: func(cCtx *cli.Context) error {
					replyTo := cCtx.Args().First()
					path := ""
					if replyTo == "" {
						path = "root.bin"
					} else {
						replyLink, err := cfg.DB.FindLinkByShortID(replyTo)
						if err != nil {
							return err
						} else {
							path = fmt.Sprintf("%s.%s", replyLink.Path, replyLink.ShortID)
						}
					}

					buf := new(strings.Builder)
					_, err = io.Copy(buf, sesh)
					if err != nil {
						return err
					}
					txt := buf.String()

					link, err := cfg.DB.CreateLink(&LinkTree{
						UserID: user.ID,
						Text:   txt,
						Path:   path,
					})
					if err != nil {
						return err
					}

					wish.Printf(sesh, "breadcrumbs: %s\n\n", path)

					writer := NewTabWriter(sesh)
					fmt.Fprintln(writer, "URL\tShortID\tPath")
					fmt.Fprintf(
						writer, "https://links.pico.sh/%s\t%s\t%s\n",
						link.ShortID,
						link.ShortID,
						link.Path,
					)
					writer.Flush()

					return nil
				},
			},
			{
				Name:    "vote",
				Aliases: []string{"v"},
				Usage:   "Toggle vote any link tree post",
				Action: func(cCtx *cli.Context) error {
					replyTo := cCtx.Args().First()
					link, err := cfg.DB.FindLinkByShortID(replyTo)
					if err != nil {
						return err
					}

					total, err := cfg.DB.Vote(link.ID, user.ID)
					if err != nil {
						return err
					}

					writer := NewTabWriter(sesh)
					fmt.Fprintln(writer, "URL\tShortID\tPath")
					fmt.Fprintf(
						writer, "https://links.pico.sh/%s\t%s\t%s\t%d\n",
						link.ShortID,
						link.ShortID,
						link.Path,
						total,
					)
					writer.Flush()

					return nil
				},
			},
		},
	}

	return app
}
