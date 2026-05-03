package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/picosh/pico/pkg/apps/auth"
	"github.com/picosh/pico/pkg/db/postgres"
)

func main() {
	monthPtr := flag.String("month", "", "target month in YYYY-MM format (default: previous month)")
	backfill := flag.Bool("backfill", false, "aggregate all historical months up to the month before last")
	dryRun := flag.Bool("dry-run", false, "print months that would be processed without running aggregation")
	flag.Parse()

	logger := slog.Default()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL must be set")
		os.Exit(1)
	}
	dbpool := postgres.NewDB(dbURL, logger)
	defer func() { _ = dbpool.Close() }()

	if *backfill {
		runBackfill(dbpool, logger, *dryRun)
		return
	}

	targetMonth := parseMonth(*monthPtr, logger)
	if err := auth.RunAnalyticsAggregation(dbpool, logger, targetMonth); err != nil {
		logger.Error("aggregation failed", "err", err)
		os.Exit(1)
	}
}

func runBackfill(dbpool *postgres.PsqlDB, logger *slog.Logger, dryRun bool) {
	months, err := fetchHistoricalMonths(dbpool)
	if err != nil {
		logger.Error("failed to fetch historical months", "err", err)
		os.Exit(1)
	}
	if dryRun {
		fmt.Println("Months to backfill:")
		for _, m := range months {
			fmt.Println(" ", m.Format("2006-01"))
		}
		return
	}
	for _, m := range months {
		if err := auth.RunAnalyticsAggregation(dbpool, logger, m); err != nil {
			logger.Error("aggregation failed for month", "month", m.Format("2006-01"), "err", err)
		}
	}
	logger.Info("backfill complete", "months", len(months))
}

// fetchHistoricalMonths returns all distinct months that have data in analytics_visits,
// excluding the current month and the previous month (handled by the auth service cron).
func fetchHistoricalMonths(dbpool *postgres.PsqlDB) ([]time.Time, error) {
	cutoff := time.Now().AddDate(0, -1, 0)
	cutoffMonth := time.Date(cutoff.Year(), cutoff.Month(), 1, 0, 0, 0, 0, time.UTC)

	rows, err := dbpool.Db.Queryx(`
		SELECT DISTINCT date_trunc('month', created_at)::date AS month_start
		FROM analytics_visits
		WHERE created_at < $1
		ORDER BY month_start ASC
	`, cutoffMonth)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var months []time.Time
	for rows.Next() {
		var monthDate time.Time
		if err := rows.Scan(&monthDate); err != nil {
			return nil, err
		}
		months = append(months, monthDate)
	}
	return months, rows.Err()
}

func parseMonth(arg string, logger *slog.Logger) time.Time {
	now := time.Now()
	if arg != "" {
		t, err := time.Parse("2006-01", arg)
		if err != nil {
			logger.Error("invalid month format, use YYYY-MM", "err", err, "input", arg)
			os.Exit(1)
		}
		return t
	}
	return now.AddDate(0, -1, 0)
}
