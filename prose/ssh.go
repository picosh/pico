package prose

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/promwish"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/filehandlers"
	uploadimgs "github.com/picosh/pico/filehandlers/imgs"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	wsh "github.com/picosh/pico/wish"
	"github.com/picosh/send/auth"
	"github.com/picosh/send/list"
	"github.com/picosh/send/pipe"
	wishrsync "github.com/picosh/send/protocols/rsync"
	"github.com/picosh/send/protocols/scp"
	"github.com/picosh/send/protocols/sftp"
	"github.com/picosh/send/proxy"
	"github.com/picosh/utils"
	pipeUtil "github.com/picosh/utils/pipe"
)

func createRouter(handler *filehandlers.FileHandlerRouter, cliHandler *CliHandler) proxy.Router {
	return func(sh ssh.Handler, s ssh.Session) []wish.Middleware {
		return []wish.Middleware{
			pipe.Middleware(handler, ".md"),
			list.Middleware(handler),
			scp.Middleware(handler),
			wishrsync.Middleware(handler),
			auth.Middleware(handler),
			wsh.PtyMdw(wsh.DeprecatedNotice()),
			WishMiddleware(cliHandler),
			wsh.LogMiddleware(handler.GetLogger()),
		}
	}
}

func withProxy(handler *filehandlers.FileHandlerRouter, cliHandler *CliHandler, otherMiddleware ...wish.Middleware) ssh.Option {
	return func(server *ssh.Server) error {
		err := sftp.SSHOption(handler)(server)
		if err != nil {
			return err
		}

		return proxy.WithProxy(createRouter(handler, cliHandler), otherMiddleware...)(server)
	}
}

func createPubProseDrain(ctx context.Context, logger *slog.Logger) *pipeUtil.ReconnectReadWriteCloser {
	info := shared.NewPicoPipeClient()
	send := pipeUtil.NewReconnectReadWriteCloser(
		ctx,
		logger,
		info,
		"pub to prose-drain",
		"pub prose-drain -b=false",
		100,
		-1,
	)
	return send
}

func StartSshServer() {
	host := utils.GetEnv("PROSE_HOST", "0.0.0.0")
	port := utils.GetEnv("PROSE_SSH_PORT", "2222")
	promPort := utils.GetEnv("PROSE_PROM_PORT", "9222")
	cfg := NewConfigSite()
	logger := cfg.Logger
	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbh.Close()

	ctx := context.Background()
	defer ctx.Done()
	pipeClient := createPubProseDrain(ctx, logger)

	hooks := &MarkdownHooks{
		Cfg:  cfg,
		Db:   dbh,
		Pipe: pipeClient,
	}

	var st storage.StorageServe
	var err error
	if cfg.MinioURL == "" {
		st, err = storage.NewStorageFS(cfg.Logger, cfg.StorageDir)
	} else {
		st, err = storage.NewStorageMinio(cfg.Logger, cfg.MinioURL, cfg.MinioUser, cfg.MinioPass)
	}

	if err != nil {
		logger.Error("storage", "err", err.Error())
		return
	}

	fileMap := map[string]filehandlers.ReadWriteHandler{
		".md":      filehandlers.NewScpPostHandler(dbh, cfg, hooks, st),
		".css":     filehandlers.NewScpPostHandler(dbh, cfg, hooks, st),
		"fallback": uploadimgs.NewUploadImgHandler(dbh, cfg, st, pipeClient),
	}
	handler := filehandlers.NewFileHandlerRouter(cfg, dbh, fileMap)
	handler.Spaces = []string{cfg.Space, "imgs"}

	cliHandler := &CliHandler{
		Logger: logger,
		DBPool: dbh,
	}

	sshAuth := shared.NewSshAuthHandler(dbh, logger, cfg)
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(sshAuth.PubkeyAuthHandler),
		withProxy(
			handler,
			cliHandler,
			promwish.Middleware(fmt.Sprintf("%s:%s", host, promPort), "prose-ssh"),
		),
	)
	if err != nil {
		logger.Error("wish server", "err", err.Error())
		return
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("Starting SSH server", "host", host, "port", port)
	go func() {
		if err = s.ListenAndServe(); err != nil {
			logger.Error(err.Error())
		}
	}()

	<-done
	logger.Info("Stopping SSH server")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer func() { cancel() }()
	if err := s.Shutdown(ctx); err != nil {
		logger.Error(err.Error())
	}
}
