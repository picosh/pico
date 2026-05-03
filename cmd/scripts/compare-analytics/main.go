package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/postgres"
)

func main() {
	host := flag.String("host", "", "host to compare (required)")
	userID := flag.String("user-id", "", "user ID to compare (required)")
	interval := flag.String("interval", "day", "interval: day, week, month")
	origin := flag.String("origin", "", "origin date in YYYY-MM-DD format (default: start of year)")
	flag.Parse()

	if *host == "" || *userID == "" {
		fmt.Fprintln(os.Stderr, "usage: compare-analytics -host example.com -user-id <uuid> [-interval day|week|month] [-origin 2025-01-01]")
		os.Exit(1)
	}

	logger := slog.Default()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL must be set")
		os.Exit(1)
	}

	dbpool := postgres.NewDB(dbURL, logger)
	defer func() { _ = dbpool.Close() }()

	originTime := time.Date(time.Now().AddDate(-1, 0, 0).Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	if *origin != "" {
		var err error
		originTime, err = time.Parse("2006-01-02", *origin)
		if err != nil {
			logger.Error("invalid origin format, use YYYY-MM-DD", "err", err)
			os.Exit(1)
		}
	}

	opts := &db.SummaryOpts{
		Host:     *host,
		UserID:   *userID,
		Interval: *interval,
		Origin:   originTime,
	}

	// New path: reads from summary tables + current month raw
	newSummary, err := dbpool.VisitSummary(opts)
	if err != nil {
		logger.Error("VisitSummary failed", "err", err)
		os.Exit(1)
	}

	// Old path: direct queries against analytics_visits
	oldSummary, err := queryRawVisits(dbpool, opts)
	if err != nil {
		logger.Error("raw query failed", "err", err)
		os.Exit(1)
	}

	// Compare
	fmt.Println("=== Unique Visitors (Intervals) ===")
	compareIntervals(newSummary.Intervals, oldSummary.Intervals)

	fmt.Println("\n=== Top URLs ===")
	compareUrls(newSummary.TopUrls, oldSummary.TopUrls)

	fmt.Println("\n=== Top Referers ===")
	compareUrls(newSummary.TopReferers, oldSummary.TopReferers)

	fmt.Println("\n=== 404 URLs ===")
	compareUrls(newSummary.NotFoundUrls, oldSummary.NotFoundUrls)
}

func queryRawVisits(dbpool *postgres.PsqlDB, opts *db.SummaryOpts) (*db.SummaryVisits, error) {
	intervals, err := rawVisitUnique(dbpool, opts)
	if err != nil {
		return nil, fmt.Errorf("raw visit unique: %w", err)
	}
	urls, err := rawVisitUrl(dbpool, opts)
	if err != nil {
		return nil, fmt.Errorf("raw visit url: %w", err)
	}
	refs, err := rawVisitReferer(dbpool, opts)
	if err != nil {
		return nil, fmt.Errorf("raw visit referer: %w", err)
	}
	notFound, err := rawVisitUrlNotFound(dbpool, opts)
	if err != nil {
		return nil, fmt.Errorf("raw visit url not found: %w", err)
	}
	return &db.SummaryVisits{
		Intervals:    intervals,
		TopUrls:      urls,
		TopReferers:  refs,
		NotFoundUrls: notFound,
	}, nil
}

func rawVisitUnique(dbpool *postgres.PsqlDB, opts *db.SummaryOpts) ([]*db.VisitInterval, error) {
	where, with := visitFilterBy(opts)
	query := fmt.Sprintf(`
		SELECT date_trunc('%s', created_at)::timestamptz as interval_start,
		       count(DISTINCT ip_address) as unique_visitors
		FROM analytics_visits
		WHERE created_at >= $1 AND %s = $2 AND user_id = $3 AND status <> 404
		GROUP BY interval_start
		ORDER BY interval_start`, opts.Interval, where)

	rows, err := dbpool.Db.Queryx(query, opts.Origin, with, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var intervals []*db.VisitInterval
	for rows.Next() {
		iv := &db.VisitInterval{}
		if err := rows.Scan(&iv.Interval, &iv.Visitors); err != nil {
			return nil, err
		}
		intervals = append(intervals, iv)
	}
	return intervals, rows.Err()
}

func rawVisitUrl(dbpool *postgres.PsqlDB, opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	where, with := visitFilterBy(opts)
	query := fmt.Sprintf(`
		SELECT path as url, count(DISTINCT ip_address) as count
		FROM analytics_visits
		WHERE created_at >= $1 AND %s = $2 AND user_id = $3 AND path <> '' AND status <> 404
		GROUP BY path
		ORDER BY count DESC
		LIMIT 10`, where)

	rows, err := dbpool.Db.Queryx(query, opts.Origin, with, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var urls []*db.VisitUrl
	for rows.Next() {
		u := &db.VisitUrl{}
		if err := rows.Scan(&u.Url, &u.Count); err != nil {
			return nil, err
		}
		urls = append(urls, u)
	}
	return urls, rows.Err()
}

func rawVisitReferer(dbpool *postgres.PsqlDB, opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	where, with := visitFilterBy(opts)
	query := fmt.Sprintf(`
		SELECT referer as url, count(DISTINCT ip_address) as count
		FROM analytics_visits
		WHERE created_at >= $1 AND %s = $2 AND user_id = $3 AND referer <> '' AND status <> 404
		GROUP BY referer
		ORDER BY count DESC
		LIMIT 10`, where)

	rows, err := dbpool.Db.Queryx(query, opts.Origin, with, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var urls []*db.VisitUrl
	for rows.Next() {
		u := &db.VisitUrl{}
		if err := rows.Scan(&u.Url, &u.Count); err != nil {
			return nil, err
		}
		urls = append(urls, u)
	}
	return urls, rows.Err()
}

func rawVisitUrlNotFound(dbpool *postgres.PsqlDB, opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	where, with := visitFilterBy(opts)
	limit := opts.Limit
	if limit == 0 {
		limit = 10
	}
	query := fmt.Sprintf(`
		SELECT path as url, count(DISTINCT ip_address) as count
		FROM analytics_visits
		WHERE created_at >= $1 AND %s = $2 AND user_id = $3 AND path <> '' AND status = 404
		GROUP BY path
		ORDER BY count DESC
		LIMIT %d`, where, limit)

	rows, err := dbpool.Db.Queryx(query, opts.Origin, with, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var urls []*db.VisitUrl
	for rows.Next() {
		u := &db.VisitUrl{}
		if err := rows.Scan(&u.Url, &u.Count); err != nil {
			return nil, err
		}
		urls = append(urls, u)
	}
	return urls, rows.Err()
}

func visitFilterBy(opts *db.SummaryOpts) (string, string) {
	if opts.Host != "" {
		return "host", opts.Host
	}
	if opts.Path != "" {
		return "path", opts.Path
	}
	return "host", ""
}

func compareIntervals(newData, oldData []*db.VisitInterval) {
	newMap := make(map[string]int)
	for _, iv := range newData {
		key := iv.Interval.Format("2006-01-02")
		newMap[key] = iv.Visitors
	}
	oldMap := make(map[string]int)
	for _, iv := range oldData {
		key := iv.Interval.Format("2006-01-02")
		oldMap[key] = iv.Visitors
	}

	// Collect all keys
	keys := make(map[string]bool)
	for k := range newMap {
		keys[k] = true
	}
	for k := range oldMap {
		keys[k] = true
	}

	mismatches := 0
	for k := range keys {
		n, nOk := newMap[k]
		o, oOk := oldMap[k]
		if nOk && oOk {
			if n != o {
				fmt.Printf("  MISMATCH %s: new=%d old=%d\n", k, n, o)
				mismatches++
			}
		} else if nOk {
			fmt.Printf("  NEW ONLY %s: %d\n", k, n)
			mismatches++
		} else {
			fmt.Printf("  OLD ONLY %s: %d\n", k, o)
			mismatches++
		}
	}
	if mismatches == 0 {
		fmt.Println("  OK: all intervals match")
	} else {
		fmt.Printf("  %d mismatches\n", mismatches)
	}
}

func compareUrls(newData, oldData []*db.VisitUrl) {
	newMap := make(map[string]int)
	for _, u := range newData {
		newMap[u.Url] = u.Count
	}
	oldMap := make(map[string]int)
	for _, u := range oldData {
		oldMap[u.Url] = u.Count
	}

	mismatches := 0
	for url, nCount := range newMap {
		if oCount, ok := oldMap[url]; ok {
			if nCount != oCount {
				fmt.Printf("  MISMATCH %s: new=%d old=%d\n", url, nCount, oCount)
				mismatches++
			}
		} else {
			fmt.Printf("  NEW ONLY %s: %d\n", url, nCount)
			mismatches++
		}
	}
	for url, oCount := range oldMap {
		if _, ok := newMap[url]; !ok {
			fmt.Printf("  OLD ONLY %s: %d\n", url, oCount)
			mismatches++
		}
	}
	if mismatches == 0 {
		fmt.Println("  OK: all entries match")
	} else {
		fmt.Printf("  %d mismatches\n", mismatches)
	}
}
