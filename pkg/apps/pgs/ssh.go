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
	"golang.org/x/crypto/ssh"
)

func StartSshServer(cfg *PgsConfig, killCh chan error) {
	host := utils.GetEnv("PGS_HOST", "0.0.0.0")
	port := utils.GetEnv("PGS_SSH_PORT", "2222")
	// promPort := utils.GetEnv("PGS_PROM_PORT", "9222")
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cacheClearingQueue := make(chan string, 100)
	handler := NewUploadAssetHandler(
		cfg,
		cacheClearingQueue,
		ctx,
	)

	sshAuth := shared.NewSshAuthHandler(cfg.DB, logger)

	webTunnel := &tunkit.WebTunnelHandler{
		Logger:      logger,
		HttpHandler: createHttpHandler(cfg),
	}

	// Create a new SSH server
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
			pssh.PtyMdw(pssh.DeprecatedNotice()),
			Middleware(handler),
			pssh.LogMiddleware(handler, handler.Cfg.DB),
		},
		SubsystemMiddleware: []pssh.SSHServerMiddleware{
			sftp.Middleware(handler),
			pssh.LogMiddleware(handler, handler.Cfg.DB),
		},
		ChannelMiddleware: map[string]pssh.SSHServerChannelMiddleware{
			"direct-tcpip": tunkit.LocalForwardHandler(webTunnel),
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

	select {
	case <-killCh:
		exit()
	case <-done:
		exit()
	}
}
