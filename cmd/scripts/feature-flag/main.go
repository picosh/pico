package main

import (
	"log/slog"
	"os"

	"github.com/picosh/pico/pkg/db/postgres"
)

func main() {
	logger := slog.Default()
	DbURL := os.Getenv("DATABASE_URL")
	dbpool := postgres.NewDB(DbURL, logger)

	args := os.Args
	if len(args) < 3 {
		logger.Error("usage: go run ./cmd/scripts/feature-flag <username> <feature>")
		os.Exit(1)
	}

	username := args[1]
	feature := args[2]

	logger.Info(
		"Adding feature flag to user",
		"username", username,
		"feature", feature,
	)

	err := dbpool.AddFeatureUser(username, feature)
	if err != nil {
		logger.Error("Failed to add feature flag to user", "err", err)
		os.Exit(1)
	} else {
		logger.Info("Successfully added feature flag to user")
	}
}
