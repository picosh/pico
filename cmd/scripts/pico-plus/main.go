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
	paymentType := ""
	if len(args) > 2 {
		paymentType = args[2]
	}
	txId := ""
	if len(args) > 3 {
		txId = args[3]
	}

	logger.Info(
		"Upgrading user to pico+",
		"username", username,
		"paymentType", paymentType,
		"txId", txId,
	)

	err := dbpool.AddPicoPlusUser(username, paymentType, txId)
	if err != nil {
		logger.Error("Failed to add pico+ user", "err", err)
		os.Exit(1)
	} else {
		logger.Info("Successfully added pico+ user")
	}
}
