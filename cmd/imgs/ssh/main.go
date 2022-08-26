package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"git.sr.ht/~erock/pico/db/postgres"
	uploadimgs "git.sr.ht/~erock/pico/filehandlers/imgs"
	"git.sr.ht/~erock/pico/imgs"
	"git.sr.ht/~erock/pico/imgs/storage"
	"git.sr.ht/~erock/pico/shared"
	"git.sr.ht/~erock/pico/wish/cms"
	"git.sr.ht/~erock/pico/wish/list"
	"git.sr.ht/~erock/pico/wish/pipe"
	"git.sr.ht/~erock/pico/wish/proxy"
	wishrsync "git.sr.ht/~erock/pico/wish/send/rsync"
	"git.sr.ht/~erock/pico/wish/send/scp"
	"git.sr.ht/~erock/pico/wish/send/sftp"
	"git.sr.ht/~erock/pico/wish/send/utils"
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

func createRouter(cfg *shared.ConfigSite, handler utils.CopyFromClientHandler) proxy.Router {
	return func(sh ssh.Handler, s ssh.Session) []wish.Middleware {
		return []wish.Middleware{
			pipe.Middleware(handler, ""),
			list.Middleware(handler),
			scp.Middleware(handler),
			wishrsync.Middleware(handler),
			bm.Middleware(cms.Middleware(&cfg.ConfigCms, cfg)),
			lm.Middleware(),
		}
	}
}

func withProxy(cfg *shared.ConfigSite, handler utils.CopyFromClientHandler, otherMiddleware ...wish.Middleware) ssh.Option {
	return func(server *ssh.Server) error {
		err := sftp.SSHOption(handler)(server)
		if err != nil {
			return err
		}

		return proxy.WithProxy(createRouter(cfg, handler), otherMiddleware...)(server)
	}
}

func main() {
	host := shared.GetEnv("IMGS_HOST", "0.0.0.0")
	port := shared.GetEnv("IMGS_SSH_PORT", "2222")
	promPort := shared.GetEnv("IMGS_PROM_PORT", "9222")
	cfg := imgs.NewConfigSite()
	logger := cfg.Logger
	dbh := postgres.NewDB(&cfg.ConfigCms)
	defer dbh.Close()

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

	handler := uploadimgs.NewUploadImgHandler(
		dbh,
		cfg,
		st,
	)

	sshServer := &SSHServer{}
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(sshServer.authHandler),
		withProxy(
			cfg,
			handler,
			promwish.Middleware(fmt.Sprintf("%s:%s", host, promPort), "pastes-ssh"),
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
