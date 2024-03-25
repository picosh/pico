package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/picosh/pico/db/postgres"
)

func main() {
	logger := slog.Default()
	DbURL := os.Getenv("DATABASE_URL")
	dbpool := postgres.NewDB(DbURL, logger)
	now := time.Now()

	stats, err := dbpool.VisitSummary(
		"5dabf12d-f0d7-44f1-9557-32a043ffac37",
		"user_id",
		"day",
		now.AddDate(0, 0, -now.Day()+1),
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
			"urls",
			"path", url.Url,
			"count", url.Count,
			"postID", url.PostID,
			"projectID", url.ProjectID,
		)
	}
}
