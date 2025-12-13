package pastes

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	"github.com/picosh/utils"
)

func StartSshServer() {
	appName := "pastes-ssh"

	host := utils.GetEnv("PASTES_HOST", "0.0.0.0")
	port := utils.GetEnv("PASTES_SSH_PORT", "2222")
	promPort := utils.GetEnv("PASTES_PROM_PORT", "9222")
	cfg := NewConfigSite(appName)
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer func() {
		_ = dbh.Close()
	}()
	hooks := &FileHooks{
		Cfg: cfg,
		Db:  dbh,
	}

	fileMap := map[string]filehandlers.ReadWriteHandler{
		"fallback": filehandlers.NewScpPostHandler(dbh, cfg, hooks),
	}
	handler := filehandlers.NewFileHandlerRouter(cfg, dbh, fileMap)
	sshAuth := shared.NewSshAuthHandler(dbh, logger, "pastes")

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
			pipe.Middleware(handler, ""),
			list.Middleware(handler),
			scp.Middleware(handler),
			rsync.Middleware(handler),
			auth.Middleware(handler),
			pssh.PtyMdw(pssh.DeprecatedNotice(), 200*time.Millisecond),
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
