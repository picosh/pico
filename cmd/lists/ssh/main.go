package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"git.sr.ht/~erock/pico/db/postgres"
	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/lists"
	"git.sr.ht/~erock/pico/shared"
	"git.sr.ht/~erock/pico/wish/cms"
	"git.sr.ht/~erock/pico/wish/pipe"
	"git.sr.ht/~erock/pico/wish/proxy"
	wishrsync "git.sr.ht/~erock/pico/wish/send/rsync"
	"git.sr.ht/~erock/pico/wish/send/scp"
	"git.sr.ht/~erock/pico/wish/send/sftp"
	"github.com/charmbracelet/promwish"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	lm "github.com/charmbracelet/wish/logging"
	"github.com/gliderlabs/ssh"
)

type SSHServer struct{}

func (me *SSHServer) authHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	return true
}

func createRouter(handler *filehandlers.ScpUploadHandler) proxy.Router {
	return func(sh ssh.Handler, s ssh.Session) []wish.Middleware {
		cmd := s.Command()
		mdw := []wish.Middleware{}

		if len(cmd) > 0 && cmd[0] == "scp" {
			mdw = append(mdw, scp.Middleware(handler))
		} else if len(cmd) > 0 && cmd[0] == "rsync" {
			mdw = append(mdw, wishrsync.Middleware(handler))
		} else {
			mdw = append(mdw,
				pipe.Middleware(handler, ".txt"),
				bm.Middleware(cms.Middleware(&handler.Cfg.ConfigCms, handler.Cfg)),
				lm.Middleware(),
			)
		}

		return mdw
	}
}

func withProxy(handler *filehandlers.ScpUploadHandler, otherMiddleware ...wish.Middleware) ssh.Option {
	return func(server *ssh.Server) error {
		err := sftp.SSHOption(handler)(server)
		if err != nil {
			return err
		}

		return proxy.WithProxy(createRouter(handler), otherMiddleware...)(server)
	}
}

func main() {
	host := shared.GetEnv("LISTS_HOST", "0.0.0.0")
	port := shared.GetEnv("LISTS_SSH_PORT", "2222")
	promPort := shared.GetEnv("LISTS_PROM_PORT", "9222")
	cfg := lists.NewConfigSite()
	logger := cfg.Logger
	dbh := postgres.NewDB(&cfg.ConfigCms)
	defer dbh.Close()

	hooks := &lists.ListHooks{
		Cfg: cfg,
	}
	handler := filehandlers.NewScpPostHandler(dbh, cfg, hooks)

	sshServer := &SSHServer{}
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(sshServer.authHandler),
		withProxy(
			handler,
			promwish.Middleware(fmt.Sprintf("%s:%s", host, promPort), "lists-ssh"),
		),
	)
	if err != nil {
		logger.Fatal(err)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	logger.Infof("Starting SSH server on %s:%s", host, port)
	go func() {
		if err = s.ListenAndServe(); err != nil {
			logger.Fatal(err)
		}
	}()

	<-done
	logger.Info("Stopping SSH server")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer func() { cancel() }()
	if err := s.Shutdown(ctx); err != nil {
		logger.Fatal(err)
	}
}
