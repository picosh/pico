package main

import (
	"log/slog"
	"os"

	"github.com/picosh/pico/db/postgres"
)

func main() {
	logger := slog.Default()
	DbURL := os.Getenv("DATABASE_URL")
	dbpool := postgres.NewDB(DbURL, logger)

	args := os.Args
	username := args[1]
	txId := args[2]

	logger.Info(
		"Upgrading user to pico+",
		"username", username,
		"txId", txId,
	)

	err := dbpool.AddPicoPlusUser(username, txId)
	if err != nil {
		logger.Error("Failed to add pico+ user", "err", err)
		os.Exit(1)
	} else {
		logger.Info("Successfully added pico+ user")
	}
}
