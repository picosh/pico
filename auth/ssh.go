package auth

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
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	// "github.com/picosh/pico/shared/storage"
	wsh "github.com/picosh/pico/wish"
)

func authHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	return true
}

func StartSshServer() {
	host := shared.GetEnv("AUTH_HOST", "0.0.0.0")
	port := shared.GetEnv("AUTH_SSH_PORT", "2222")
	promPort := shared.GetEnv("AUTH_PROM_PORT", "9222")
	cfg := NewConfigSite()
	logger := cfg.Logger
	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbh.Close()

	/*
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
	*/

	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(authHandler),
		wish.WithMiddleware(
			// WishMiddleware(dbh, logger, st),
			bm.Middleware(CmsMiddleware(&cfg.ConfigCms, cfg)),
			wsh.LogMiddleware(cfg.Logger),
			promwish.Middleware(fmt.Sprintf("%s:%s", host, promPort), "auth-ssh"),
		),
	)
	if err != nil {
		logger.Error(err.Error())
		return
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("Starting SSH server on", "host", host, "port", port)
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
