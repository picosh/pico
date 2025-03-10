package main

import (
	"context"
	"os"

	"github.com/picosh/pico/pgs"
	pgsdb "github.com/picosh/pico/pgs/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/send/auth"
	"github.com/picosh/send/list"
	"github.com/picosh/send/pipe"
	"github.com/picosh/send/protocols/scp"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
)

func main() {
	// Initialize the logger
	logger := shared.CreateLogger("pgs-ssh")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	minioURL := utils.GetEnv("MINIO_URL", "")
	minioUser := utils.GetEnv("MINIO_ROOT_USER", "")
	minioPass := utils.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := utils.GetEnv("DATABASE_URL", "")

	dbpool, err := pgsdb.NewDB(dbURL, logger)
	if err != nil {
		panic(err)
	}

	st, err := storage.NewStorageMinio(logger, minioURL, minioUser, minioPass)
	if err != nil {
		panic(err)
	}

	cfg := pgs.NewPgsConfig(logger, dbpool, st)

	sshAuth := shared.NewSshAuthHandler(cfg.DB, logger)

	cacheClearingQueue := make(chan string, 100)

	handler := pgs.NewUploadAssetHandler(
		cfg,
		cacheClearingQueue,
		ctx,
	)

	// Create a new SSH server
	server := shared.NewSSHServer(ctx, logger, &shared.SSHServerConfig{
		ListenAddr: "localhost:2222",
		ServerConfig: &ssh.ServerConfig{
			PublicKeyCallback: sshAuth.PubkeyAuthHandler,
		},
		Middleware: []shared.SSHServerMiddleware{
			pipe.Middleware(handler, ""),
			list.Middleware(handler),
			scp.Middleware(handler),
			wishrsync.Middleware(handler),
			auth.Middleware(handler),
			wsh.PtyMdw(wsh.DeprecatedNotice()),
			WishMiddleware(handler),
			wsh.LogMiddleware(handler.GetLogger(s), handler.Cfg.DB),
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

	err = server.ListenAndServe()
	if err != nil {
		logger.Error("failed to start SSH server", "error", err)
		return
	}

	logger.Info("SSH server started successfully")
}
