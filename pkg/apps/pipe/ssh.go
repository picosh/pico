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
	"github.com/picosh/utils"
)

func StartSshServer() {
	host := utils.GetEnv("PIPE_HOST", "0.0.0.0")
	port := utils.GetEnv("PIPE_SSH_PORT", "2222")
	portOverride := utils.GetEnv("PIPE_SSH_PORT_OVERRIDE", port)
	promPort := utils.GetEnv("PIPE_PROM_PORT", "9222")
	cfg := NewConfigSite("pipe-ssh")
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbh.Close()

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

	sshAuth := shared.NewSshAuthHandler(dbh, logger)

	// Create a new SSH server
	server, err := pssh.NewSSHServerWithConfig(
		ctx,
		logger,
		host,
		port,
		promPort,
		sshAuth.PubkeyAuthHandler,
		[]pssh.SSHServerMiddleware{
			WishMiddleware(handler),
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
	logger.Info("Starting SSH server", "host", host, "port", port)
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
