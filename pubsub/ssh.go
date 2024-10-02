package pubsub

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/charmbracelet/promwish"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/filehandlers/util"
	"github.com/picosh/pico/shared"
	wsh "github.com/picosh/pico/wish"
	psub "github.com/picosh/pubsub"
)

func StartSshServer() {
	host := shared.GetEnv("PUBSUB_HOST", "0.0.0.0")
	port := shared.GetEnv("PUBSUB_SSH_PORT", "2222")
	promPort := shared.GetEnv("PUBSUB_PROM_PORT", "9222")
	cfg := NewConfigSite()
	logger := cfg.Logger
	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbh.Close()

	cfg.Port = port

	pubsub := &psub.Cfg{
		Logger: logger,
		PubSub: &psub.PubSubMulticast{
			Logger: logger,
			Connector: &psub.BaseConnector{
				Channels: syncmap.New[string, *psub.Channel](),
			},
		},
	}

	handler := &CliHandler{
		Logger: logger,
		DBPool: dbh,
		PubSub: pubsub,
		Cfg:    cfg,
	}

	sshAuth := util.NewSshAuthHandler(dbh, logger, cfg)
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(sshAuth.PubkeyAuthHandler),
		wish.WithMiddleware(
			WishMiddleware(handler),
			promwish.Middleware(fmt.Sprintf("%s:%s", host, promPort), "pubsub-ssh"),
			wsh.LogMiddleware(logger),
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
