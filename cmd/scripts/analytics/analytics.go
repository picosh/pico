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

	stats, err := dbpool.VisitSummary(
		"5dabf12d-f0d7-44f1-9557-32a043ffac37",
		"user_id",
		"1 day",
		"month",
	)
	if err != nil {
		panic(err)
	}
	logger.Info("summary", "stats", stats)
}
