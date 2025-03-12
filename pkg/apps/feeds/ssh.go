package feeds

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/auth"
	"github.com/picosh/pico/pkg/send/list"
	"github.com/picosh/pico/pkg/send/pipe"
	"github.com/picosh/pico/pkg/send/protocols/rsync"
	"github.com/picosh/pico/pkg/send/protocols/scp"
	"github.com/picosh/pico/pkg/send/protocols/sftp"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
)

func StartSshServer() {
	host := utils.GetEnv("LISTS_HOST", "0.0.0.0")
	port := utils.GetEnv("LISTS_SSH_PORT", "2222")
	// promPort := utils.GetEnv("LISTS_PROM_PORT", "9222")
	cfg := NewConfigSite("feeds-ssh")
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbh.Close()

	hooks := &FeedHooks{
		Cfg: cfg,
		Db:  dbh,
	}

	fileMap := map[string]filehandlers.ReadWriteHandler{
		"fallback": filehandlers.NewScpPostHandler(dbh, cfg, hooks),
	}
	handler := filehandlers.NewFileHandlerRouter(cfg, dbh, fileMap)

	sshAuth := shared.NewSshAuthHandler(dbh, logger)
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{
		ListenAddr: "localhost:2222",
		ServerConfig: &ssh.ServerConfig{
			PublicKeyCallback: sshAuth.PubkeyAuthHandler,
		},
		Middleware: []pssh.SSHServerMiddleware{
			pipe.Middleware(handler, ".txt"),
			list.Middleware(handler),
			scp.Middleware(handler),
			rsync.Middleware(handler),
			auth.Middleware(handler),
			pssh.PtyMdw(pssh.DeprecatedNotice()),
			pssh.LogMiddleware(handler, dbh),
		},
		SubsystemMiddleware: []pssh.SSHServerMiddleware{
			sftp.Middleware(handler),
			pssh.LogMiddleware(handler, dbh),
		},
	})

	pemBytes, err := os.ReadFile("ssh_data/term_info_ed25519")
	if err != nil {
		logger.Error("failed to read private key file", "error", err)
		return
	}

	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		logger.Error("failed to parse private key", "error", err)
		return
	}

	server.Config.AddHostKey(signer)

	done := make(chan os.Signal, 1)

	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("Starting SSH server", "host", host, "port", port)
	go func() {
		if err = server.ListenAndServe(); err != nil {
			logger.Error("serve", "err", err.Error())
			os.Exit(1)
		}
	}()

	exit := func() {
		logger.Info("stopping ssh server")
		cancel()
	}

	<-done
	exit()
}
