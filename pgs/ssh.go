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
	"github.com/picosh/pico/shared"
	wsh "github.com/picosh/pico/wish"
	"github.com/picosh/send/auth"
	"github.com/picosh/send/list"
	"github.com/picosh/send/pipe"
	wishrsync "github.com/picosh/send/protocols/rsync"
	"github.com/picosh/send/protocols/scp"
	"github.com/picosh/send/protocols/sftp"
	"github.com/picosh/send/proxy"
	"github.com/picosh/tunkit"
	"github.com/picosh/utils"
)

func createRouter(handler *UploadAssetHandler) proxy.Router {
	return func(sh ssh.Handler, s ssh.Session) []wish.Middleware {
		return []wish.Middleware{
			pipe.Middleware(handler, ""),
			list.Middleware(handler),
			scp.Middleware(handler),
			wishrsync.Middleware(handler),
			auth.Middleware(handler),
			wsh.PtyMdw(wsh.DeprecatedNotice()),
			WishMiddleware(handler),
			wsh.LogMiddleware(handler.GetLogger(s), handler.Cfg.DB),
		}
	}
}

func withProxy(handler *UploadAssetHandler, otherMiddleware ...wish.Middleware) ssh.Option {
	return func(server *ssh.Server) error {
		err := sftp.SSHOption(handler)(server)
		if err != nil {
			return err
		}

		newSubsystemHandlers := map[string]ssh.SubsystemHandler{}

		for name, subsystemHandler := range server.SubsystemHandlers {
			newSubsystemHandlers[name] = func(s ssh.Session) {
				wsh.LogMiddleware(handler.GetLogger(s), handler.Cfg.DB)(ssh.Handler(subsystemHandler))(s)
			}
		}

		server.SubsystemHandlers = newSubsystemHandlers

		return proxy.WithProxy(createRouter(handler), otherMiddleware...)(server)
	}
}

func StartSshServer(cfg *PgsConfig, killCh chan error) {
	host := utils.GetEnv("PGS_HOST", "0.0.0.0")
	port := utils.GetEnv("PGS_SSH_PORT", "2222")
	promPort := utils.GetEnv("PGS_PROM_PORT", "9222")
	logger := cfg.Logger

	ctx := context.Background()
	defer ctx.Done()

	cacheClearingQueue := make(chan string, 100)
	handler := NewUploadAssetHandler(
		cfg,
		cacheClearingQueue,
		ctx,
	)

	webTunnel := &tunkit.WebTunnelHandler{
		Logger:      logger,
		HttpHandler: createHttpHandler(cfg),
	}

	sshAuth := shared.NewSshAuthHandler(cfg.DB, logger)
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(sshAuth.PubkeyAuthHandler),
		tunkit.WithWebTunnel(webTunnel),
		withProxy(
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
			if err != ssh.ErrServerClosed {
				logger.Error("serve", "err", err.Error())
				os.Exit(1)
			}
		}
	}()

	exit := func() {
		logger.Info("stopping ssh server")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer func() { cancel() }()
		if err := s.Shutdown(ctx); err != nil {
			logger.Error("shutdown", "err", err.Error())
			os.Exit(1)
		}
	}

	select {
	case <-killCh:
		exit()
	case <-done:
		exit()
	}
}
