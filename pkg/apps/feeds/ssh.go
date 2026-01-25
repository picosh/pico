package feeds

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/auth"
	"github.com/picosh/pico/pkg/send/list"
	"github.com/picosh/pico/pkg/send/pipe"
	"github.com/picosh/pico/pkg/send/protocols/rsync"
	"github.com/picosh/pico/pkg/send/protocols/scp"
	"github.com/picosh/pico/pkg/send/protocols/sftp"
	"github.com/picosh/pico/pkg/shared"
)

func StartSshServer() {
	appName := "feeds-ssh"

	host := shared.GetEnv("FEEDS_HOST", "0.0.0.0")
	port := shared.GetEnv("FEEDS_SSH_PORT", "2222")
	promPort := shared.GetEnv("FEEDS_PROM_PORT", "9222")
	cfg := NewConfigSite(appName)
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer func() {
		_ = dbh.Close()
	}()

	hooks := &FeedHooks{
		Cfg: cfg,
		Db:  dbh,
	}

	fileMap := map[string]filehandlers.ReadWriteHandler{
		"fallback": filehandlers.NewScpPostHandler(dbh, cfg, hooks),
	}
	handler := filehandlers.NewFileHandlerRouter(cfg, dbh, fileMap)

	sshAuth := shared.NewSshAuthHandler(dbh, logger, "feeds")

	// Create a new SSH server
	server, err := pssh.NewSSHServerWithConfig(
		ctx,
		logger,
		appName,
		host,
		port,
		promPort,
		"ssh_data/term_info_ed25519",
		sshAuth.PubkeyAuthHandler,
		[]pssh.SSHServerMiddleware{
			pipe.Middleware(handler, ".txt"),
			list.Middleware(handler),
			scp.Middleware(handler),
			rsync.Middleware(handler),
			auth.Middleware(handler),
			Middleware(dbh, cfg),
			pssh.LogMiddleware(handler, dbh),
		},
		[]pssh.SSHServerMiddleware{
			sftp.Middleware(handler),
			pssh.LogMiddleware(handler, dbh),
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
