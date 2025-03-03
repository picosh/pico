package main

import (
	"context"
	"log/slog"

	"github.com/picosh/pico/shared"
)

func main() {
	// Initialize the logger
	logger := slog.Default()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a new SSH server
	server := shared.NewSSHServer(ctx, logger, &shared.SSHServerConfig{
		ListenAddr: "localhost:2222",
	})

	err := server.ListenAndServe()
	if err != nil {
		logger.Error("failed to start SSH server", "error", err)
		return
	}

	logger.Info("SSH server started successfully")
}
