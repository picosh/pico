package pico

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/auth"
	"github.com/picosh/pico/pkg/send/list"
	"github.com/picosh/pico/pkg/send/pipe"
	"github.com/picosh/pico/pkg/send/protocols/rsync"
	"github.com/picosh/pico/pkg/send/protocols/scp"
	"github.com/picosh/pico/pkg/send/protocols/sftp"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/tui"
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
	appName := "pico-ssh"

	host := utils.GetEnv("PICO_HOST", "0.0.0.0")
	port := utils.GetEnv("PICO_SSH_PORT", "2222")
	promPort := utils.GetEnv("PICO_PROM_PORT", "9222")
	cfg := NewConfigSite(appName)
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

	// Create a new SSH server
	server, err := pssh.NewSSHServerWithConfig(
		ctx,
		logger,
		appName,
		host,
		port,
		promPort,
		func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			perms, _ := sshAuth.PubkeyAuthHandler(conn, key)
			if perms == nil {
				perms = &ssh.Permissions{
					Extensions: map[string]string{
						"pubkey": utils.KeyForKeyText(key),
					},
				}
			}

			return perms, nil
		},
		[]pssh.SSHServerMiddleware{
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
		[]pssh.SSHServerMiddleware{
			sftp.Middleware(handler),
			pssh.LogMiddleware(handler, dbpool),
		},
		nil,
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

	<-done
	exit()
}
