package pastes

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
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/filehandlers"
	"github.com/picosh/pico/shared"
	wsh "github.com/picosh/pico/wish"
	"github.com/picosh/send/auth"
	"github.com/picosh/send/list"
	"github.com/picosh/send/pipe"
	wishrsync "github.com/picosh/send/protocols/rsync"
	"github.com/picosh/send/protocols/scp"
	"github.com/picosh/send/proxy"
	"github.com/picosh/utils"
)

func createRouter(handler *filehandlers.FileHandlerRouter) proxy.Router {
	return func(sh ssh.Handler, s ssh.Session) []wish.Middleware {
		return []wish.Middleware{
			pipe.Middleware(handler, ""),
			list.Middleware(handler),
			scp.Middleware(handler),
			wishrsync.Middleware(handler),
			auth.Middleware(handler),
			wsh.PtyMdw(wsh.DeprecatedNotice()),
			wsh.LogMiddleware(handler.GetLogger(s), handler.DBPool),
		}
	}
}

func withProxy(handler *filehandlers.FileHandlerRouter, otherMiddleware ...wish.Middleware) ssh.Option {
	return func(server *ssh.Server) error {
		newSubsystemHandlers := map[string]ssh.SubsystemHandler{}

		for name, subsystemHandlers := range server.SubsystemHandlers {
			newSubsystemHandlers[name] = func(s ssh.Session) {
				wsh.LogMiddleware(handler.GetLogger(s), handler.DBPool)(ssh.Handler(subsystemHandlers))
			}
		}

		server.SubsystemHandlers = newSubsystemHandlers

		return proxy.WithProxy(createRouter(handler), otherMiddleware...)(server)
	}
}

func StartSshServer() {
	host := utils.GetEnv("PASTES_HOST", "0.0.0.0")
	port := utils.GetEnv("PASTES_SSH_PORT", "2222")
	promPort := utils.GetEnv("PASTES_PROM_PORT", "9222")
	cfg := NewConfigSite("pastes-ssh")
	logger := cfg.Logger
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
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(sshAuth.PubkeyAuthHandler),
		withProxy(
			handler,
			promwish.Middleware(fmt.Sprintf("%s:%s", host, promPort), "pastes-ssh"),
		),
	)
	if err != nil {
		logger.Error(err.Error())
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
