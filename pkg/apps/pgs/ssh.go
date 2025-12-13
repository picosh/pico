package pgs

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/auth"
	"github.com/picosh/pico/pkg/send/list"
	"github.com/picosh/pico/pkg/send/pipe"
	"github.com/picosh/pico/pkg/send/protocols/rsync"
	"github.com/picosh/pico/pkg/send/protocols/scp"
	"github.com/picosh/pico/pkg/send/protocols/sftp"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/tunkit"
	"github.com/picosh/utils"
)

func StartSshServer(cfg *PgsConfig, killCh chan error) {
	host := utils.GetEnv("PGS_HOST", "0.0.0.0")
	port := utils.GetEnv("PGS_SSH_PORT", "2222")
	promPort := utils.GetEnv("PGS_PROM_PORT", "9222")
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cacheClearingQueue := make(chan string, 100)
	handler := NewUploadAssetHandler(
		cfg,
		cacheClearingQueue,
		ctx,
	)

	sshAuth := shared.NewSshAuthHandler(cfg.DB, logger, "pgs")

	webTunnel := &tunkit.WebTunnelHandler{
		Logger:      logger,
		HttpHandler: CreateHttpHandler(cfg),
	}

	// Create a new SSH server
	server, err := pssh.NewSSHServerWithConfig(
		ctx,
		logger,
		"pgs-ssh",
		host,
		port,
		promPort,
		"ssh_data/term_info_ed25519",
		sshAuth.PubkeyAuthHandler,
		[]pssh.SSHServerMiddleware{
			pipe.Middleware(handler, ""),
			list.Middleware(handler),
			scp.Middleware(handler),
			rsync.Middleware(handler),
			auth.Middleware(handler),
			Middleware(handler),
			pssh.LogMiddleware(handler, handler.Cfg.DB),
		},
		[]pssh.SSHServerMiddleware{
			sftp.Middleware(handler),
			pssh.LogMiddleware(handler, handler.Cfg.DB),
		},
		map[string]pssh.SSHServerChannelMiddleware{
			"direct-tcpip": tunkit.LocalForwardHandler(webTunnel),
		},
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
