package main

import (
	"log/slog"
	"os"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/utils"
)

func main() {
	logger := slog.Default()
	DbURL := os.Getenv("DATABASE_URL")
	dbpool := postgres.NewDB(DbURL, logger)

	args := os.Args
	fkID := args[1]

	stats, err := dbpool.VisitSummary(
		&db.SummaryOpts{
			FkID: fkID,
			// By:   "post_id",
			By:       "user_id",
			Interval: "day",
			Origin:   utils.StartOfMonth(),
			// Where:    "AND (post_id IS NOT NULL OR (post_id IS NULL AND project_id IS NULL))",
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
			"url", url.Url,
			"count", url.Count,
			"postID", url.PostID,
			"projectID", url.ProjectID,
		)
	}

	for _, url := range stats.TopReferers {
		logger.Info(
			"referer",
			"url", url.Url,
			"count", url.Count,
			"postID", url.PostID,
			"projectID", url.ProjectID,
		)
	}
}
