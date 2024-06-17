package pgs

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/promwish"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	uploadassets "github.com/picosh/pico/filehandlers/assets"
	"github.com/picosh/pico/filehandlers/util"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	wsh "github.com/picosh/pico/wish"
	"github.com/picosh/ptun"
	"github.com/picosh/send/list"
	"github.com/picosh/send/pipe"
	"github.com/picosh/send/proxy"
	"github.com/picosh/send/send/auth"
	wishrsync "github.com/picosh/send/send/rsync"
	"github.com/picosh/send/send/scp"
	"github.com/picosh/send/send/sftp"
)

func createRouter(cfg *shared.ConfigSite, handler *uploadassets.UploadAssetHandler) proxy.Router {
	return func(sh ssh.Handler, s ssh.Session) []wish.Middleware {
		return []wish.Middleware{
			pipe.Middleware(handler, ""),
			list.Middleware(handler),
			scp.Middleware(handler),
			wishrsync.Middleware(handler),
			auth.Middleware(handler),
			wsh.PtyMdw(wsh.DeprecatedNotice()),
			WishMiddleware(handler),
			wsh.LogMiddleware(handler.GetLogger()),
		}
	}
}

func withProxy(cfg *shared.ConfigSite, handler *uploadassets.UploadAssetHandler, otherMiddleware ...wish.Middleware) ssh.Option {
	return func(server *ssh.Server) error {
		err := sftp.SSHOption(handler)(server)
		if err != nil {
			return err
		}

		return proxy.WithProxy(createRouter(cfg, handler), otherMiddleware...)(server)
	}
}

func StartSshServer() {
	host := shared.GetEnv("PGS_HOST", "0.0.0.0")
	port := shared.GetEnv("PGS_SSH_PORT", "2222")
	promPort := shared.GetEnv("PGS_PROM_PORT", "9222")
	cfg := NewConfigSite()
	logger := cfg.Logger
	dbpool := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbpool.Close()

	var st storage.StorageServe
	var err error
	if cfg.MinioURL == "" {
		st, err = storage.NewStorageFS(cfg.StorageDir)
	} else {
		st, err = storage.NewStorageMinio(cfg.MinioURL, cfg.MinioUser, cfg.MinioPass)
	}

	if err != nil {
		logger.Error(err.Error())
		return
	}

	handler := uploadassets.NewUploadAssetHandler(
		dbpool,
		cfg,
		st,
	)

	ch := make(chan *db.AnalyticsVisits)
	go shared.AnalyticsCollect(ch, dbpool, logger)
	apiConfig := &shared.ApiConfig{
		Cfg:            cfg,
		Dbpool:         dbpool,
		Storage:        st,
		AnalyticsQueue: ch,
	}

	webTunnel := &ptun.WebTunnelHandler{
		Logger:      logger,
		HttpHandler: createHttpHandler(apiConfig),
	}

	sshAuth := util.NewSshAuthHandler(dbpool, logger, cfg)
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(sshAuth.PubkeyAuthHandler),
		ptun.WithWebTunnel(webTunnel),
		withProxy(
			cfg,
			handler,
			promwish.Middleware(fmt.Sprintf("%s:%s", host, promPort), "pgs-ssh"),
		),
	)
	if err != nil {
		logger.Error(err.Error())
		return
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("starting SSH server on", "host", host, "port", port)
	go func() {
		if err = s.ListenAndServe(); err != nil {
			logger.Error("serve", "err", err.Error())
			os.Exit(1)
		}
	}()

	<-done
	logger.Info("stopping SSH server")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer func() { cancel() }()
	if err := s.Shutdown(ctx); err != nil {
		logger.Error("shutdown", "err", err.Error())
		os.Exit(1)
	}
}
