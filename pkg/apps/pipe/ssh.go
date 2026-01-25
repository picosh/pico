package pipe

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/antoniomika/syncmap"
	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	psub "github.com/picosh/pubsub"
	"golang.org/x/crypto/ssh"
)

func StartSshServer() {
	appName := "pipe-ssh"

	host := shared.GetEnv("PIPE_HOST", "0.0.0.0")
	port := shared.GetEnv("PIPE_SSH_PORT", "2222")
	portOverride := shared.GetEnv("PIPE_SSH_PORT_OVERRIDE", port)
	promPort := shared.GetEnv("PIPE_PROM_PORT", "9222")
	cfg := NewConfigSite(appName)
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer func() {
		_ = dbh.Close()
	}()

	cfg.Port = port
	cfg.PortOverride = portOverride

	pubsub := psub.NewMulticast(logger)
	handler := &CliHandler{
		Logger:  logger,
		DBPool:  dbh,
		PubSub:  pubsub,
		Cfg:     cfg,
		Waiters: syncmap.New[string, []string](),
		Access:  syncmap.New[string, []string](),
	}

	sshAuth := shared.NewSshAuthHandler(dbh, logger, "pipe")

	// Create a new SSH server
	server, err := pssh.NewSSHServerWithConfig(
		ctx,
		logger,
		appName,
		host,
		port,
		promPort,
		"ssh_data/term_info_ed25519",
		func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			perms, _ := sshAuth.PubkeyAuthHandler(conn, key)
			if perms == nil {
				perms = &ssh.Permissions{
					Extensions: map[string]string{
						"pubkey": shared.KeyForKeyText(key),
					},
				}
			}

			return perms, nil
		},
		[]pssh.SSHServerMiddleware{
			Middleware(handler),
			pssh.LogMiddleware(handler, dbh),
		},
		nil,
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
