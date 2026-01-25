package prose

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/filehandlers"
	uploadimgs "github.com/picosh/pico/pkg/filehandlers/imgs"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/auth"
	"github.com/picosh/pico/pkg/send/list"
	"github.com/picosh/pico/pkg/send/pipe"
	"github.com/picosh/pico/pkg/send/protocols/rsync"
	"github.com/picosh/pico/pkg/send/protocols/scp"
	"github.com/picosh/pico/pkg/send/protocols/sftp"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/storage"
)

func StartSshServer() {
	appName := "prose-ssh"

	host := shared.GetEnv("PROSE_HOST", "0.0.0.0")
	port := shared.GetEnv("PROSE_SSH_PORT", "2222")
	promPort := shared.GetEnv("PROSE_PROM_PORT", "9222")
	cfg := NewConfigSite(appName)
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer func() {
		_ = dbh.Close()
	}()

	hooks := &MarkdownHooks{
		Cfg: cfg,
		Db:  dbh,
	}

	adapter := storage.GetStorageTypeFromEnv()
	st, err := storage.NewStorage(cfg.Logger, adapter)
	if err != nil {
		logger.Error("loading storage", "err", err)
		return
	}

	fileMap := map[string]filehandlers.ReadWriteHandler{
		".md":      filehandlers.NewScpPostHandler(dbh, cfg, hooks),
		".txt":     filehandlers.NewScpPostHandler(dbh, cfg, hooks),
		".css":     filehandlers.NewScpPostHandler(dbh, cfg, hooks),
		".lxt":     filehandlers.NewScpPostHandler(dbh, cfg, hooks),
		"fallback": uploadimgs.NewUploadImgHandler(dbh, cfg, st),
	}
	handler := filehandlers.NewFileHandlerRouter(cfg, dbh, fileMap)

	sshAuth := shared.NewSshAuthHandler(dbh, logger, "prose")

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
			pipe.Middleware(handler, ".md"),
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
