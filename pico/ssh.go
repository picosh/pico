package pico

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/pssh"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui"
	"github.com/picosh/send/auth"
	"github.com/picosh/send/list"
	"github.com/picosh/send/pipe"
	"github.com/picosh/send/protocols/rsync"
	"github.com/picosh/send/protocols/scp"
	"github.com/picosh/send/protocols/sftp"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
)

func createTui(shrd *tui.SharedModel) pssh.SSHServerMiddleware {
	return func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(sesh *pssh.SSHServerConnSession) error {
			vty, err := shared.NewVConsole(sesh)
			if err != nil {
				return err
			}
			opts := vaxis.Options{
				WithConsole: vty,
			}
			tui.NewTui(opts, shrd)
			return nil
		}
	}
}

func StartSshServer() {
	host := utils.GetEnv("PICO_HOST", "0.0.0.0")
	port := utils.GetEnv("PICO_SSH_PORT", "2222")
	// promPort := utils.GetEnv("PICO_PROM_PORT", "9222")
	cfg := NewConfigSite("pico-ssh")
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbpool := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbpool.Close()

	handler := NewUploadHandler(
		dbpool,
		cfg,
	)

	cliHandler := &CliHandler{
		Logger: logger,
		DBPool: dbpool,
	}

	sshAuth := shared.NewSshAuthHandler(dbpool, logger)
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{
		ListenAddr: "localhost:2222",
		ServerConfig: &ssh.ServerConfig{
			PublicKeyCallback: sshAuth.PubkeyAuthHandler,
		},
		Middleware: []pssh.SSHServerMiddleware{
			pipe.Middleware(handler, ""),
			list.Middleware(handler),
			scp.Middleware(handler),
			rsync.Middleware(handler),
			auth.Middleware(handler),
			func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
				return func(sesh *pssh.SSHServerConnSession) error {
					shrd := &tui.SharedModel{
						Session: sesh,
						Cfg:     cfg,
						Dbpool:  handler.DBPool,
						Logger:  cfg.Logger,
					}
					return pssh.PtyMdw(createTui(shrd))(next)(sesh)
				}
			},
			WishMiddleware(cliHandler),
			pssh.LogMiddleware(handler, dbpool),
		},
		SubsystemMiddleware: []pssh.SSHServerMiddleware{
			sftp.Middleware(handler),
			pssh.LogMiddleware(handler, dbpool),
		},
	})

	pemBytes, err := os.ReadFile("ssh_data/term_info_ed25519")
	if err != nil {
		logger.Error("failed to read private key file", "error", err)
		return
	}

	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		logger.Error("failed to parse private key", "error", err)
		return
	}

	server.Config.AddHostKey(signer)

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("starting SSH server on", "host", host, "port", port)
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

	<-done
	exit()
}
