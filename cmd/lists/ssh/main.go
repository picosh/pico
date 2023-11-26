package main

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
	lm "github.com/charmbracelet/wish/logging"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/filehandlers"
	"github.com/picosh/pico/lists"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/cms"
	"github.com/picosh/send/list"
	"github.com/picosh/send/pipe"
	"github.com/picosh/send/proxy"
	"github.com/picosh/send/send/auth"
	wishrsync "github.com/picosh/send/send/rsync"
	"github.com/picosh/send/send/scp"
	"github.com/picosh/send/send/sftp"
)

type SSHServer struct{}

func (me *SSHServer) authHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	return true
}

func createRouter(handler *filehandlers.ScpUploadHandler) proxy.Router {
	return func(sh ssh.Handler, s ssh.Session) []wish.Middleware {
		return []wish.Middleware{
			pipe.Middleware(handler, ".txt"),
			list.Middleware(handler),
			scp.Middleware(handler),
			wishrsync.Middleware(handler),
			auth.Middleware(handler),
			bm.Middleware(cms.Middleware(&handler.Cfg.ConfigCms, handler.Cfg)),
			lm.Middleware(),
		}
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
	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbh.Close()

	hooks := &lists.ListHooks{
		Cfg: cfg,
		Db:  dbh,
	}

	var st storage.ObjectStorage
	var err error
	if cfg.MinioURL == "" {
		st, err = storage.NewStorageFS(cfg.StorageDir)
	} else {
		st, err = storage.NewStorageMinio(cfg.MinioURL, cfg.MinioUser, cfg.MinioPass)
	}

	if err != nil {
		logger.Fatal(err)
	}

	handler := filehandlers.NewScpPostHandler(dbh, cfg, hooks, st)

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
