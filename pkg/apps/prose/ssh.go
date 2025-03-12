package prose

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/filehandlers"
	uploadimgs "github.com/picosh/pico/pkg/filehandlers/imgs"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/auth"
	"github.com/picosh/pico/pkg/send/list"
	"github.com/picosh/pico/pkg/send/pipe"
	"github.com/picosh/pico/pkg/send/protocols/rsync"
	"github.com/picosh/pico/pkg/send/protocols/scp"
	"github.com/picosh/pico/pkg/send/protocols/sftp"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/storage"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
)

func StartSshServer() {
	host := utils.GetEnv("PROSE_HOST", "0.0.0.0")
	port := utils.GetEnv("PROSE_SSH_PORT", "2222")
	// promPort := utils.GetEnv("PROSE_PROM_PORT", "9222")
	cfg := NewConfigSite("prose-ssh")
	logger := cfg.Logger

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbh.Close()

	hooks := &MarkdownHooks{
		Cfg: cfg,
		Db:  dbh,
	}

	var st storage.StorageServe
	var err error
	if cfg.MinioURL == "" {
		st, err = storage.NewStorageFS(cfg.Logger, cfg.StorageDir)
	} else {
		st, err = storage.NewStorageMinio(cfg.Logger, cfg.MinioURL, cfg.MinioUser, cfg.MinioPass)
	}

	if err != nil {
		logger.Error("storage", "err", err.Error())
		return
	}

	fileMap := map[string]filehandlers.ReadWriteHandler{
		".md":      filehandlers.NewScpPostHandler(dbh, cfg, hooks),
		".css":     filehandlers.NewScpPostHandler(dbh, cfg, hooks),
		"fallback": uploadimgs.NewUploadImgHandler(dbh, cfg, st),
	}
	handler := filehandlers.NewFileHandlerRouter(cfg, dbh, fileMap)

	sshAuth := shared.NewSshAuthHandler(dbh, logger)
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{
		ListenAddr: "localhost:2222",
		ServerConfig: &ssh.ServerConfig{
			PublicKeyCallback: sshAuth.PubkeyAuthHandler,
		},
		Middleware: []pssh.SSHServerMiddleware{
			pipe.Middleware(handler, ".md"),
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
