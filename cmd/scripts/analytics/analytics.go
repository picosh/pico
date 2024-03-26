package main

import (
	"log/slog"
	"os"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
)

func main() {
	logger := slog.Default()
	DbURL := os.Getenv("DATABASE_URL")
	dbpool := postgres.NewDB(DbURL, logger)

	args := os.Args
	userID := args[1]

	stats, err := dbpool.VisitSummary(
		&db.SummarOpts{
			FkID:     userID,
			By:       "user_id",
			Interval: "day",
			Origin:   shared.StartOfMonth(),
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
			"postID", s.PostID,
			"projectID", s.ProjectID,
		)
	}

	for _, url := range stats.TopUrls {
		logger.Info(
			"url",
			"path", url.Url,
			"count", url.Count,
			"postID", url.PostID,
			"projectID", url.ProjectID,
		)
	}

	for _, url := range stats.TopReferers {
		logger.Info(
			"referer",
			"path", url.Url,
			"count", url.Count,
			"postID", url.PostID,
			"projectID", url.ProjectID,
		)
	}
}
