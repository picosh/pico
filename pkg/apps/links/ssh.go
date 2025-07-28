package links

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils"
	"github.com/urfave/cli"
)

type CfgLogger struct{}

func (h *CfgLogger) GetLogger(s *pssh.SSHServerConnSession) *slog.Logger {
	return pssh.GetLogger(s)
}

func StartSshServer(cfg *LinksConfig, killCh chan error) {
	logger := cfg.Logger
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sshAuth := shared.NewSshAuthHandler(cfg.DB, logger)
	server, err := pssh.NewSSHServerWithConfig(
		ctx,
		logger,
		"links-ssh",
		cfg.SshHost,
		cfg.SshPort,
		cfg.PromPort,
		"ssh_data/term_info_ed25519",
		sshAuth.PubkeyAuthHandler,
		[]pssh.SSHServerMiddleware{
			CliMiddleware(cfg),
			pssh.LogMiddleware(&CfgLogger{}, cfg.DB),
		},
		[]pssh.SSHServerMiddleware{},
		map[string]pssh.SSHServerChannelMiddleware{},
	)

	if err != nil {
		logger.Error("failed to create ssh server", "err", err.Error())
		os.Exit(1)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("Starting SSH server", "addr", server.Config.ListenAddr)
	go func() {
		if err = server.ListenAndServe(); err != nil {
			logger.Error("serve", "err", err.Error())
			os.Exit(1)
		}
	}()

	exit := func() {
		logger.Info("stopping ssh server")
		cancel()
	}

	select {
	case <-killCh:
		exit()
	case <-done:
		exit()
	}
}

func CliMiddleware(cfg *LinksConfig) pssh.SSHServerMiddleware {
	return func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(sesh *pssh.SSHServerConnSession) error {
			args := sesh.Command()
			cli := NewCli(sesh, cfg)
			margs := append([]string{"links"}, args...)
			err := cli.Run(margs)
			if err != nil {
				cfg.Logger.Error("error when running cli", "err", err)
				sesh.Fatal(fmt.Errorf("err: %w", err))
				next(sesh)
				return err
			}
			return nil
		}
	}
}

func NewTabWriter(out io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 0, 1, ' ', tabwriter.TabIndent)
}

func NewCli(sesh *pssh.SSHServerConnSession, cfg *LinksConfig) *cli.App {
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
				sesh.Fatal(fmt.Errorf("err: %w", err))
			}
		},
		OnUsageError: func(cCtx *cli.Context, err error, isSubcommand bool) error {
			if err != nil {
				sesh.Fatal(fmt.Errorf("err: %w", err))
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

					orig := strings.Split(path, ".")
					for _, link := range links.Data {
						sp := strings.Split(link.Path, ".")
						ident := len(sp) - len(orig)
						col := strings.Repeat(" ", int(ident))
						sesh.Println(
							fmt.Sprintf(
								"%s|%d %s %s.%s",
								col, link.Votes, link.Username, link.Path, link.ShortID,
							),
						)
						lines := strings.Split(link.Text, "\n")
						for _, line := range lines {
							sesh.Println(fmt.Sprintf("%s| %s", col, line))
						}
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
