package pipe

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/charmbracelet/promwish"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	wsh "github.com/picosh/pico/wish"
	psub "github.com/picosh/pubsub"
	"github.com/picosh/utils"
)

func StartSshServer() {
	host := utils.GetEnv("PIPE_HOST", "0.0.0.0")
	port := utils.GetEnv("PIPE_SSH_PORT", "2222")
	portOverride := utils.GetEnv("PIPE_SSH_PORT_OVERRIDE", port)
	promPort := utils.GetEnv("PIPE_PROM_PORT", "9222")
	cfg := NewConfigSite("pipe-ssh")
	logger := cfg.Logger
	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbh.Close()

	cfg.Port = port
	cfg.PortOverride = portOverride

	pubsub := psub.NewMulticast(logger)
	handler := &CliHandler{
		Logger:  logger,
		DBPool:  dbh,
		PubSub:  pubsub,
		Cfg:     cfg,
		Waiters: syncmap.New[string, []string](),
		Access:  syncmap.New[string, []string](),
	}

	sshAuth := shared.NewSshAuthHandler(dbh, logger)
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
			sshAuth.PubkeyAuthHandler(ctx, key)
			return true
		}),
		wish.WithMiddleware(
			WishMiddleware(handler),
			promwish.Middleware(fmt.Sprintf("%s:%s", host, promPort), "pipe-ssh"),
			wsh.LogMiddleware(logger, dbh),
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
			logger.Error("listen", "err", err.Error())
		}
	}()

	<-done
	logger.Info("Stopping SSH server")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer func() { cancel() }()
	if err := s.Shutdown(ctx); err != nil {
		logger.Error("shutdown", "err", err.Error())
	}
}
