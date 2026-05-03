package auth

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/db/postgres"
)

// visitRow represents a single raw visit record for aggregation.
type visitRow struct {
	UserID    string
	Host      string
	Path      string
	IPAddress string
	Referer   string
	Status    int
	UserAgent string
	CreatedAt time.Time
}

// dayStats accumulates per-day visitor counts for a (user_id, host) pair.
type dayStats struct {
	Date       time.Time
	AllIPs     map[string]bool
	MobileIPs  map[string]bool
	DesktopIPs map[string]bool
}

// pathStats accumulates per-path visitor counts for a (user_id, host, status_code) triplet.
type pathStats struct {
	Path   string
	Status int
	IPs    map[string]bool
}

// userHostPair represents a unique (user_id, host) combination.
type userHostPair struct {
	UserID string
	Host   string
}

// RunAnalyticsAggregation aggregates raw visits for the given month into summary tables.
// Exported for use by the analytics-aggregate CLI script.
func RunAnalyticsAggregation(dbpool *postgres.PsqlDB, logger *slog.Logger, targetMonth time.Time) error {
	monthStart := time.Date(targetMonth.Year(), targetMonth.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	logger.Info("starting analytics aggregation", "month", targetMonth.Format("2006-01"), "from", monthStart, "to", monthEnd)

	pairs, err := fetchUserHostPairs(dbpool, monthStart, monthEnd)
	if err != nil {
		return fmt.Errorf("fetch user/host pairs: %w", err)
	}
	if len(pairs) == 0 {
		logger.Info("no user/host pairs to aggregate", "month", targetMonth.Format("2006-01"))
		return nil
	}
	logger.Info("found user/host pairs to process", "count", len(pairs), "month", targetMonth.Format("2006-01"))

	for _, pair := range pairs {
		visits, err := fetchVisitsForPair(dbpool, pair.UserID, pair.Host, monthStart, monthEnd)
		if err != nil {
			logger.Error("failed to fetch visits", "err", err, "user_id", pair.UserID, "host", pair.Host)
			continue
		}

		// Aggregate daily stats
		dayMap := aggregateDays(visits)
		if err := insertMonthlyVisits(dbpool, pair.UserID, pair.Host, dayMap); err != nil {
			logger.Error("failed to insert monthly visits", "err", err)
			continue
		}

		// Aggregate top URLs grouped by status code
		topURLsByStatus := aggregatePaths(visits)
		for status, paths := range topURLsByStatus {
			if err := insertTopURLs(dbpool, pair.UserID, pair.Host, targetMonth, status, paths); err != nil {
				logger.Error("failed to insert top URLs", "err", err, "status", status)
				continue
			}
		}

		// Aggregate top referers
		topReferers := aggregateReferers(visits)
		if err := insertTopReferers(dbpool, pair.UserID, pair.Host, targetMonth, topReferers); err != nil {
			logger.Error("failed to insert top referers", "err", err)
			continue
		}

		// Upsert user site
		totalUnique := countTotalUnique(dayMap)
		if err := upsertUserSite(dbpool, pair.UserID, pair.Host, totalUnique, targetMonth); err != nil {
			logger.Error("failed to upsert user site", "err", err)
			continue
		}
	}

	// Delete raw visit data for users with analytics enabled
	if err := deleteAggregatedVisits(dbpool, logger, monthStart, monthEnd); err != nil {
		logger.Error("failed to delete aggregated visits", "err", err, "month", targetMonth.Format("2006-01"))
	}

	logger.Info("analytics aggregation complete", "month", targetMonth.Format("2006-01"))
	return nil
}

// analyticsAggregationCron runs once per day to check if the previous month needs aggregation.
func analyticsAggregationCron(ctx context.Context, dbpool *postgres.PsqlDB, logger *slog.Logger) {
	// Check at midnight UTC every day
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Run immediately on startup to catch up if the service was down
	if err := checkAndRunAggregation(dbpool, logger); err != nil {
		logger.Error("startup analytics aggregation check failed", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("analytics aggregation cron stopped")
			return
		case <-ticker.C:
			if err := checkAndRunAggregation(dbpool, logger); err != nil {
				logger.Error("analytics aggregation check failed", "err", err)
			}
		}
	}
}

// checkAndRunAggregation checks if the previous month has been aggregated and runs if needed.
func checkAndRunAggregation(dbpool *postgres.PsqlDB, logger *slog.Logger) error {
	prevMonth := time.Now().AddDate(0, -1, 0)
	monthStart := time.Date(prevMonth.Year(), prevMonth.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	// Check if any data exists for this month in the summary table
	var count int
	err := dbpool.Db.QueryRowContext(context.Background(),
		`SELECT COUNT(DISTINCT host) FROM analytics_monthly_visits WHERE visit_date >= $1 AND visit_date < $2`,
		monthStart, monthEnd,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check existing aggregation: %w", err)
	}

	if count > 0 {
		logger.Info("previous month already aggregated, skipping", "month", prevMonth.Format("2006-01"), "hosts", count)
		return nil
	}

	logger.Info("previous month not aggregated, running now", "month", prevMonth.Format("2006-01"))
	return RunAnalyticsAggregation(dbpool, logger, prevMonth)
}

func fetchUserHostPairs(dbpool *postgres.PsqlDB, monthStart, monthEnd time.Time) ([]userHostPair, error) {
	rows, err := dbpool.Db.Queryx(
		`SELECT DISTINCT user_id, host FROM analytics_visits WHERE created_at >= $1 AND created_at < $2 AND host <> '' ORDER BY user_id, host`,
		monthStart, monthEnd,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var pairs []userHostPair
	for rows.Next() {
		var p userHostPair
		if err := rows.Scan(&p.UserID, &p.Host); err != nil {
			return nil, err
		}
		pairs = append(pairs, p)
	}
	return pairs, rows.Err()
}

func fetchVisitsForPair(dbpool *postgres.PsqlDB, userID, host string, monthStart, monthEnd time.Time) ([]visitRow, error) {
	rows, err := dbpool.Db.Queryx(
		`SELECT user_id, host, path, ip_address, referer, status, user_agent, created_at
		 FROM analytics_visits
		 WHERE user_id = $1 AND host = $2 AND created_at >= $3 AND created_at < $4`,
		userID, host, monthStart, monthEnd,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var visits []visitRow
	for rows.Next() {
		var v visitRow
		if err := rows.Scan(&v.UserID, &v.Host, &v.Path, &v.IPAddress, &v.Referer, &v.Status, &v.UserAgent, &v.CreatedAt); err != nil {
			return nil, err
		}
		visits = append(visits, v)
	}
	return visits, rows.Err()
}

func aggregateDays(visits []visitRow) map[string]*dayStats {
	dayMap := make(map[string]*dayStats)

	for _, v := range visits {
		// Exclude 404s from unique visitor counts — bots hitting non-existent
		// paths shouldn't count as visitors
		if v.Status == 404 {
			continue
		}

		dateKey := v.CreatedAt.UTC().Format("2006-01-02")
		ds, ok := dayMap[dateKey]
		if !ok {
			ds = &dayStats{
				Date:       time.Date(v.CreatedAt.Year(), v.CreatedAt.Month(), v.CreatedAt.Day(), 0, 0, 0, 0, time.UTC),
				AllIPs:     make(map[string]bool),
				MobileIPs:  make(map[string]bool),
				DesktopIPs: make(map[string]bool),
			}
			dayMap[dateKey] = ds
		}

		ds.AllIPs[v.IPAddress] = true
		if isMobile(v.UserAgent) {
			ds.MobileIPs[v.IPAddress] = true
		} else {
			ds.DesktopIPs[v.IPAddress] = true
		}
	}
	return dayMap
}

func aggregatePaths(visits []visitRow) map[int][]pathStats {
	// Maps status_code -> list of pathStats
	statusMap := make(map[int]map[string]*pathStats)

	for _, v := range visits {
		if v.Path == "" {
			continue
		}

		if _, ok := statusMap[v.Status]; !ok {
			statusMap[v.Status] = make(map[string]*pathStats)
		}

		key := fmt.Sprintf("%s/%d", v.Path, v.Status)
		ps, ok := statusMap[v.Status][key]
		if !ok {
			ps = &pathStats{Path: v.Path, Status: v.Status, IPs: make(map[string]bool)}
			statusMap[v.Status][key] = ps
		}
		ps.IPs[v.IPAddress] = true
	}

	result := make(map[int][]pathStats)
	for status, paths := range statusMap {
		list := make([]pathStats, 0, len(paths))
		for _, ps := range paths {
			list = append(list, *ps)
		}
		sortByCount(list)
		result[status] = list
	}
	return result
}

func aggregateReferers(visits []visitRow) []pathStats {
	refMap := make(map[string]*pathStats)

	for _, v := range visits {
		if v.Referer == "" || v.Status == 404 {
			continue
		}

		rs, ok := refMap[v.Referer]
		if !ok {
			rs = &pathStats{Path: v.Referer, IPs: make(map[string]bool)}
			refMap[v.Referer] = rs
		}
		rs.IPs[v.IPAddress] = true
	}

	result := make([]pathStats, 0, len(refMap))
	for _, rs := range refMap {
		result = append(result, *rs)
	}
	sortByCount(result)
	return result
}

func sortByCount(paths []pathStats) {
	for i := 1; i < len(paths); i++ {
		for j := i; j > 0 && len(paths[j].IPs) > len(paths[j-1].IPs); j-- {
			paths[j], paths[j-1] = paths[j-1], paths[j]
		}
	}
}

func insertMonthlyVisits(dbpool *postgres.PsqlDB, userID, host string, dayMap map[string]*dayStats) error {
	for _, ds := range dayMap {
		_, err := dbpool.Db.Exec(
			`INSERT INTO analytics_monthly_visits (user_id, host, visit_date, unique_visits, mobile_visits, desktop_visits)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (user_id, host, visit_date) DO UPDATE
			 SET unique_visits = EXCLUDED.unique_visits,
			     mobile_visits = EXCLUDED.mobile_visits,
			     desktop_visits = EXCLUDED.desktop_visits`,
			userID, host, ds.Date,
			len(ds.AllIPs), len(ds.MobileIPs), len(ds.DesktopIPs),
		)
		if err != nil {
			return fmt.Errorf("insert monthly visits for %s: %w", ds.Date.Format("2006-01-02"), err)
		}
	}
	return nil
}

func insertTopURLs(dbpool *postgres.PsqlDB, userID, host string, month time.Time, statusCode int, paths []pathStats) error {
	limit := 10
	if statusCode == 404 {
		limit = 100
	}
	if len(paths) > limit {
		paths = paths[:limit]
	}

	for rank, ps := range paths {
		_, err := dbpool.Db.Exec(
			`INSERT INTO analytics_monthly_top_urls (user_id, host, month, path, unique_visits, status_code, rank)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (user_id, host, month, path, status_code) DO UPDATE
			 SET unique_visits = EXCLUDED.unique_visits,
			     rank = EXCLUDED.rank`,
			userID, host, month, ps.Path, len(ps.IPs), statusCode, rank+1,
		)
		if err != nil {
			return fmt.Errorf("insert top url %s: %w", ps.Path, err)
		}
	}
	return nil
}

func insertTopReferers(dbpool *postgres.PsqlDB, userID, host string, month time.Time, refs []pathStats) error {
	limit := 10
	if len(refs) > limit {
		refs = refs[:limit]
	}

	for rank, rs := range refs {
		_, err := dbpool.Db.Exec(
			`INSERT INTO analytics_monthly_top_referers (user_id, host, month, referer, unique_visits, rank)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (user_id, host, month, referer) DO UPDATE
			 SET unique_visits = EXCLUDED.unique_visits,
			     rank = EXCLUDED.rank`,
			userID, host, month, rs.Path, len(rs.IPs), rank+1,
		)
		if err != nil {
			return fmt.Errorf("insert top referer %s: %w", rs.Path, err)
		}
	}
	return nil
}

func countTotalUnique(dayMap map[string]*dayStats) int {
	allIPs := make(map[string]bool)
	for _, ds := range dayMap {
		for ip := range ds.AllIPs {
			allIPs[ip] = true
		}
	}
	return len(allIPs)
}

func upsertUserSite(dbpool *postgres.PsqlDB, userID, host string, monthUnique int, month time.Time) error {
	_, err := dbpool.Db.Exec(
		`INSERT INTO analytics_user_sites (user_id, host, total_visits, last_seen)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id, host) DO UPDATE
		 SET total_visits = analytics_user_sites.total_visits + EXCLUDED.total_visits,
		     last_seen = EXCLUDED.last_seen`,
		userID, host, monthUnique, month,
	)
	return err
}

// isMobile checks common mobile user-agent patterns.
// TODO: replace with a proper UA parser library (e.g., mssola/useragent).
func isMobile(userAgent string) bool {
	ua := strings.ToLower(userAgent)
	mobileKeywords := []string{
		"mobile",
		"android",
		"iphone",
		"ipad",
		"ipod",
		"blackberry",
		"windows phone",
	}
	for _, kw := range mobileKeywords {
		if strings.Contains(ua, kw) {
			return true
		}
	}
	return false
}

// deleteAggregatedVisits deletes raw visit data from analytics_visits for the given month.
// Data is preserved in summary tables, so this is safe regardless of feature flags.
// Raw data for the current and previous months is never deleted since visitUniqueFromRaw
// still reads from analytics_visits for those months.
func deleteAggregatedVisits(dbpool *postgres.PsqlDB, logger *slog.Logger, monthStart, monthEnd time.Time) error {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	// Never delete raw data for the current or previous month —
	// visitUniqueFromRaw still reads from analytics_visits for those months.
	if !monthStart.Before(previousMonthStart) {
		logger.Info("skipping raw visit deletion for recent month", "month_start", monthStart.Format("2006-01"))
		return nil
	}

	result, err := dbpool.Db.Exec(`
		DELETE FROM analytics_visits
		WHERE created_at >= $1 AND created_at < $2`, monthStart, monthEnd)
	if err != nil {
		return fmt.Errorf("delete aggregated visits: %w", err)
	}

	deleted, _ := result.RowsAffected()
	logger.Info("deleted aggregated visits", "month_start", monthStart.Format("2006-01"), "deleted", deleted)
	return nil
}
