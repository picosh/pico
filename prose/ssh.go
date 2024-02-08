package prose

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
	uploadimgs "github.com/picosh/pico/filehandlers/imgs"
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

func createRouter(handler *filehandlers.FileHandlerRouter) proxy.Router {
	return func(sh ssh.Handler, s ssh.Session) []wish.Middleware {
		return []wish.Middleware{
			pipe.Middleware(handler, ".md"),
			list.Middleware(handler),
			scp.Middleware(handler),
			wishrsync.Middleware(handler),
			auth.Middleware(handler),
			bm.Middleware(cms.Middleware(&handler.Cfg.ConfigCms, handler.Cfg)),
			lm.Middleware(),
		}
	}
}

func withProxy(handler *filehandlers.FileHandlerRouter, otherMiddleware ...wish.Middleware) ssh.Option {
	return func(server *ssh.Server) error {
		err := sftp.SSHOption(handler)(server)
		if err != nil {
			return err
		}

		return proxy.WithProxy(createRouter(handler), otherMiddleware...)(server)
	}
}

func StartSshServer() {
	host := shared.GetEnv("PROSE_HOST", "0.0.0.0")
	port := shared.GetEnv("PROSE_SSH_PORT", "2222")
	promPort := shared.GetEnv("PROSE_PROM_PORT", "9222")
	cfg := NewConfigSite()
	logger := cfg.Logger
	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbh.Close()
	hooks := &MarkdownHooks{
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

	fileMap := map[string]filehandlers.ReadWriteHandler{
		".md":      filehandlers.NewScpPostHandler(dbh, cfg, hooks, st),
		".css":     filehandlers.NewScpPostHandler(dbh, cfg, hooks, st),
		"fallback": uploadimgs.NewUploadImgHandler(dbh, cfg, st),
	}
	handler := filehandlers.NewFileHandlerRouter(cfg, dbh, fileMap)
	handler.Spaces = []string{cfg.Space, "imgs"}

	sshServer := &SSHServer{}
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(sshServer.authHandler),
		withProxy(
			handler,
			promwish.Middleware(fmt.Sprintf("%s:%s", host, promPort), "prose-ssh"),
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
