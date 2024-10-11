package pico

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
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/muesli/termenv"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui"
	wsh "github.com/picosh/pico/wish"
	"github.com/picosh/send/auth"
	"github.com/picosh/send/list"
	"github.com/picosh/send/pipe"
	wishrsync "github.com/picosh/send/protocols/rsync"
	"github.com/picosh/send/protocols/scp"
	"github.com/picosh/send/protocols/sftp"
	"github.com/picosh/send/proxy"
	"github.com/picosh/utils"
)

func authHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	shared.SetPublicKey(ctx, key)
	return true
}

func createRouter(cfg *shared.ConfigSite, handler *UploadHandler, cliHandler *CliHandler) proxy.Router {
	return func(sh ssh.Handler, s ssh.Session) []wish.Middleware {
		return []wish.Middleware{
			pipe.Middleware(handler, ""),
			list.Middleware(handler),
			scp.Middleware(handler),
			wishrsync.Middleware(handler),
			auth.Middleware(handler),
			wsh.PtyMdw(bm.MiddlewareWithColorProfile(tui.CmsMiddleware(cfg), termenv.ANSI256)),
			WishMiddleware(cliHandler),
			wsh.LogMiddleware(handler.GetLogger()),
		}
	}
}

func withProxy(cfg *shared.ConfigSite, handler *UploadHandler, cliHandler *CliHandler, otherMiddleware ...wish.Middleware) ssh.Option {
	return func(server *ssh.Server) error {
		err := sftp.SSHOption(handler)(server)
		if err != nil {
			return err
		}

		return proxy.WithProxy(createRouter(cfg, handler, cliHandler), otherMiddleware...)(server)
	}
}

func StartSshServer() {
	host := utils.GetEnv("PICO_HOST", "0.0.0.0")
	port := utils.GetEnv("PICO_SSH_PORT", "2222")
	promPort := utils.GetEnv("PICO_PROM_PORT", "9222")
	cfg := NewConfigSite()
	logger := cfg.Logger
	dbpool := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbpool.Close()

	handler := NewUploadHandler(
		dbpool,
		cfg,
	)
	cliHandler := &CliHandler{
		Logger: logger,
		DBPool: dbpool,
	}

	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(authHandler),
		withProxy(
			cfg,
			handler,
			cliHandler,
			promwish.Middleware(fmt.Sprintf("%s:%s", host, promPort), "pico-ssh"),
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
