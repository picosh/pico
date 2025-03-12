package pastes

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
	"github.com/picosh/utils"
)

func StartSshServer() {
	host := utils.GetEnv("PASTES_HOST", "0.0.0.0")
	port := utils.GetEnv("PASTES_SSH_PORT", "2222")
	promPort := utils.GetEnv("PASTES_PROM_PORT", "9222")
	cfg := NewConfigSite("pastes-ssh")
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbh.Close()
	hooks := &FileHooks{
		Cfg: cfg,
		Db:  dbh,
	}

	fileMap := map[string]filehandlers.ReadWriteHandler{
		"fallback": filehandlers.NewScpPostHandler(dbh, cfg, hooks),
	}
	handler := filehandlers.NewFileHandlerRouter(cfg, dbh, fileMap)
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
			pipe.Middleware(handler, ""),
			list.Middleware(handler),
			scp.Middleware(handler),
			rsync.Middleware(handler),
			auth.Middleware(handler),
			pssh.PtyMdw(pssh.DeprecatedNotice()),
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
