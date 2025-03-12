package main

import (
	"log/slog"
	"os"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/utils"
)

func main() {
	logger := slog.Default()
	DbURL := os.Getenv("DATABASE_URL")
	dbpool := postgres.NewDB(DbURL, logger)

	args := os.Args
	host := args[1]

	stats, err := dbpool.VisitSummary(
		&db.SummaryOpts{
			Origin: utils.StartOfMonth(),
			Host:   host,
		},
	)
	if err != nil {
		panic(err)
	}

	for _, s := range stats.Intervals {
		logger.Info(
			"interval",
			"interval", s.Interval,
			"visitors", s.Visitors,
		)
	}

	for _, url := range stats.TopUrls {
		logger.Info(
			"url",
			"url", url.Url,
			"count", url.Count,
		)
	}

	for _, url := range stats.TopReferers {
		logger.Info(
			"referer",
			"url", url.Url,
			"count", url.Count,
		)
	}
}
