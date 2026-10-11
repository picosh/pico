package postgres

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/picosh/pico/pkg/db"
)

// mobileUserAgentExpr is a SQL expression to detect mobile user agents.
const mobileUserAgentExpr = `user_agent ILIKE '%mobile%' OR user_agent ILIKE '%android%' OR user_agent ILIKE '%iphone%' OR user_agent ILIKE '%ipad%' OR user_agent ILIKE '%ipod%' OR user_agent ILIKE '%blackberry%' OR user_agent ILIKE '%windows phone%'`

func (me *PsqlDB) InsertVisit(visit *db.AnalyticsVisits) error {
	_, err := me.Db.Exec(
		`INSERT INTO analytics_visits (user_id, project_id, post_id, namespace, host, path, ip_address, user_agent, referer, status, content_type) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);`,
		visit.UserID,
		newNullString(visit.ProjectID),
		newNullString(visit.PostID),
		newNullString(visit.Namespace),
		visit.Host,
		visit.Path,
		visit.IpAddress,
		visit.UserAgent,
		visit.Referer,
		visit.Status,
		visit.ContentType,
	)
	return err
}

func visitFilterBy(opts *db.SummaryOpts) (string, string) {
	where := ""
	val := ""
	if opts.Host != "" {
		where = "host"
		val = opts.Host
	} else if opts.Path != "" {
		where = "path"
		val = opts.Path
	}

	return where, val
}

func (me *PsqlDB) visitUnique(opts *db.SummaryOpts) ([]*db.VisitInterval, error) {
	var intervals, currentIntervals []*db.VisitInterval
	var sumErr, rawErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		intervals, sumErr = me.visitUniqueFromSummary(opts)
	}()
	go func() {
		defer wg.Done()
		currentIntervals, rawErr = me.visitUniqueFromRaw(opts)
	}()

	wg.Wait()

	if sumErr != nil {
		return nil, fmt.Errorf("query summary visits: %w", sumErr)
	}
	if rawErr != nil {
		return nil, fmt.Errorf("query raw visits: %w", rawErr)
	}

	// Merge: current month data may overlap with summary data, combine counts
	return mergeVisitIntervals(intervals, currentIntervals), nil
}

// visitUniqueFromSummary reads unique visitor counts from analytics_monthly_visits for historical data.
func (me *PsqlDB) visitUniqueFromSummary(opts *db.SummaryOpts) ([]*db.VisitInterval, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	// If origin is in the previous month or later, raw data covers it — no summary to fetch.
	if !opts.Origin.Before(previousMonthStart) {
		return nil, nil
	}

	where := ""
	args := []interface{}{opts.UserID, opts.Origin, currentMonthStart}
	argIdx := 4
	if opts.Host != "" {
		where = "AND host = $" + fmt.Sprintf("%d", argIdx)
		args = append(args, opts.Host)
	}

	query := fmt.Sprintf(`
		SELECT
			date_trunc('%s', visit_date)::timestamptz as interval_start,
			sum(unique_visits) as unique_visitors,
			sum(mobile_visits) as mobile_visits,
			sum(desktop_visits) as desktop_visits
		FROM analytics_monthly_visits
		WHERE user_id = $1 AND visit_date >= $2 AND visit_date < $3 %s
		GROUP BY interval_start
		ORDER BY interval_start`, opts.Interval, where)

	rows, err := me.Db.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var intervals []*db.VisitInterval
	for rows.Next() {
		interval := &db.VisitInterval{}
		if err := rows.Scan(&interval.Interval, &interval.Visitors, &interval.MobileVisitors, &interval.DesktopVisitors); err != nil {
			return nil, err
		}
		intervals = append(intervals, interval)
	}
	return intervals, rows.Err()
}

// visitUniqueFromRaw reads unique visitor counts from analytics_visits for the previous and current months.
// This covers the gap between the last aggregated month and the current month.
func (me *PsqlDB) visitUniqueFromRaw(opts *db.SummaryOpts) ([]*db.VisitInterval, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	where, with := visitFilterBy(opts)

	// Determine the effective start: max(origin, previousMonthStart)
	effectiveStart := previousMonthStart
	if opts.Origin.After(previousMonthStart) {
		effectiveStart = opts.Origin
	}

	uniqueVisitors := fmt.Sprintf(`
		SELECT
			date_trunc('%s', created_at)::timestamptz as interval_start,
			count(DISTINCT CASE WHEN %s THEN ip_address END) as mobile_visitors,
			count(DISTINCT CASE WHEN NOT %s THEN ip_address END) as desktop_visitors,
			count(DISTINCT ip_address) as unique_visitors
		FROM analytics_visits
		WHERE created_at >= $1 AND created_at < $2 AND %s = $3 AND user_id = $4 AND status <> 404
		GROUP BY interval_start
		ORDER BY interval_start`,
		opts.Interval, mobileUserAgentExpr, mobileUserAgentExpr, where)

	rows, err := me.Db.Queryx(uniqueVisitors, effectiveStart, currentMonthStart.AddDate(0, 1, 0), with, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var intervals []*db.VisitInterval
	for rows.Next() {
		interval := &db.VisitInterval{}
		if err := rows.Scan(&interval.Interval, &interval.MobileVisitors, &interval.DesktopVisitors, &interval.Visitors); err != nil {
			return nil, err
		}
		intervals = append(intervals, interval)
	}
	return intervals, rows.Err()
}

// mergeVisitIntervals combines historical (summary table) and current (raw) intervals.
// Summary data is preferred when both sources have the same interval to avoid double-counting.
func mergeVisitIntervals(historical, current []*db.VisitInterval) []*db.VisitInterval {
	if len(historical) == 0 {
		return current
	}
	if len(current) == 0 {
		return historical
	}

	// Build a map by interval timestamp for merging.
	// Summary data takes precedence over raw data for the same interval
	// (e.g. when a month is aggregated but raw data still exists in a local dump).
	intervalMap := make(map[int64]*db.VisitInterval)
	for _, ci := range current {
		ts := ci.Interval.Unix()
		intervalMap[ts] = ci
	}
	for _, hi := range historical {
		ts := hi.Interval.Unix()
		intervalMap[ts] = hi // summary overwrites raw if both exist
	}

	// Sort by interval
	result := make([]*db.VisitInterval, 0, len(intervalMap))
	for _, iv := range intervalMap {
		result = append(result, iv)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Interval.Before(*result[j].Interval)
	})
	return result
}

// mergeTopUrls combines historical and current top URLs, summing counts for overlapping URLs,
// then returns the top 10 by total count.
func mergeTopUrls(historical, current []*db.VisitUrl) []*db.VisitUrl {
	if len(historical) == 0 {
		return current
	}
	if len(current) == 0 {
		return historical
	}

	// Build a map by URL for merging
	urlMap := make(map[string]*db.VisitUrl)
	for _, hu := range historical {
		urlMap[hu.Url] = hu
	}

	for _, cu := range current {
		if existing, ok := urlMap[cu.Url]; ok {
			existing.Count += cu.Count
		} else {
			urlMap[cu.Url] = cu
		}
	}

	// Sort by count descending and take top 10
	result := make([]*db.VisitUrl, 0, len(urlMap))
	for _, u := range urlMap {
		result = append(result, u)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Count > result[j].Count
	})
	if len(result) > 10 {
		result = result[:10]
	}
	return result
}

// mergeTopReferers combines historical and current top referers, summing counts for overlapping referers,
// then returns the top 10 by total count.
func mergeTopReferers(historical, current []*db.VisitUrl) []*db.VisitUrl {
	return mergeTopUrls(historical, current) // Same logic as mergeTopUrls
}

// mergeHosts combines historical and current hosts, summing counts for overlapping hosts,
// then returns sorted by total count descending.
func mergeHosts(historical, current []*db.VisitUrl) []*db.VisitUrl {
	if len(historical) == 0 {
		return current
	}
	if len(current) == 0 {
		return historical
	}

	// Build a map by host for merging
	hostMap := make(map[string]*db.VisitUrl)
	for _, hu := range historical {
		hostMap[hu.Url] = hu
	}

	for _, cu := range current {
		if existing, ok := hostMap[cu.Url]; ok {
			existing.Count += cu.Count
		} else {
			hostMap[cu.Url] = cu
		}
	}

	// Sort by count descending
	result := make([]*db.VisitUrl, 0, len(hostMap))
	for _, h := range hostMap {
		result = append(result, h)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Count > result[j].Count
	})
	return result
}

func (me *PsqlDB) visitReferer(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	var historical, current []*db.VisitUrl
	var histErr, rawErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		historical, histErr = me.visitRefererFromSummary(opts)
	}()
	go func() {
		defer wg.Done()
		current, rawErr = me.visitRefererFromRaw(opts)
	}()

	wg.Wait()

	if histErr != nil {
		return nil, fmt.Errorf("query summary referers: %w", histErr)
	}
	if rawErr != nil {
		return nil, fmt.Errorf("query raw referers: %w", rawErr)
	}

	return mergeTopReferers(historical, current), nil
}

// visitRefererFromSummary reads top referers from analytics_monthly_top_referers for historical data.
func (me *PsqlDB) visitRefererFromSummary(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	// If origin is in the previous month or later, raw data covers it — no summary to fetch.
	if !opts.Origin.Before(previousMonthStart) {
		return nil, nil
	}

	// Clamp origin to month boundary for summary table lookup
	originMonthStart := time.Date(opts.Origin.Year(), opts.Origin.Month(), 1, 0, 0, 0, 0, time.UTC)

	where := ""
	args := []interface{}{opts.UserID, originMonthStart, currentMonthStart}
	if opts.Host != "" {
		where = "AND host = $4"
		args = append(args, opts.Host)
	}

	query := fmt.Sprintf(`
		SELECT referer, sum(unique_visits) as total_visits
		FROM analytics_monthly_top_referers
		WHERE user_id = $1 AND month >= $2 AND month < $3 %s
		GROUP BY referer
		ORDER BY total_visits DESC
		LIMIT 10`, where)

	rows, err := me.Db.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// visitRefererFromRaw reads top referers from analytics_visits for the previous and current months.
func (me *PsqlDB) visitRefererFromRaw(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	where, with := visitFilterBy(opts)

	// Determine the effective start: max(origin, previousMonthStart)
	effectiveStart := previousMonthStart
	if opts.Origin.After(previousMonthStart) {
		effectiveStart = opts.Origin
	}

	topUrls := fmt.Sprintf(`
		SELECT
			referer,
			count(DISTINCT ip_address) as referer_count
		FROM analytics_visits
		WHERE created_at >= $1 AND created_at < $2 AND %s = $3 AND user_id = $4 AND referer <> '' AND status <> 404
		GROUP BY referer
		ORDER BY referer_count DESC
		LIMIT 10`, where)

	rows, err := me.Db.Queryx(topUrls, effectiveStart, currentMonthStart.AddDate(0, 1, 0), with, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (me *PsqlDB) visitUrl(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	var historical, current []*db.VisitUrl
	var histErr, rawErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		historical, histErr = me.visitUrlFromSummary(opts)
	}()
	go func() {
		defer wg.Done()
		current, rawErr = me.visitUrlFromRaw(opts)
	}()

	wg.Wait()

	if histErr != nil {
		return nil, fmt.Errorf("query summary urls: %w", histErr)
	}
	if rawErr != nil {
		return nil, fmt.Errorf("query raw urls: %w", rawErr)
	}

	return mergeTopUrls(historical, current), nil
}

// visitUrlFromSummary reads top URLs from analytics_monthly_top_urls for historical data.
func (me *PsqlDB) visitUrlFromSummary(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	// If origin is in the previous month or later, raw data covers it — no summary to fetch.
	if !opts.Origin.Before(previousMonthStart) {
		return nil, nil
	}

	// Clamp origin to month boundary for summary table lookup
	originMonthStart := time.Date(opts.Origin.Year(), opts.Origin.Month(), 1, 0, 0, 0, 0, time.UTC)

	where := ""
	args := []interface{}{opts.UserID, originMonthStart, currentMonthStart}
	if opts.Host != "" {
		where = "AND host = $4"
		args = append(args, opts.Host)
	}

	query := fmt.Sprintf(`
		SELECT path, sum(unique_visits) as total_visits
		FROM analytics_monthly_top_urls
		WHERE user_id = $1 AND month >= $2 AND month < $3 AND status_code <> 404 %s
		GROUP BY path
		ORDER BY total_visits DESC
		LIMIT 10`, where)

	rows, err := me.Db.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// visitUrlFromRaw reads top URLs from analytics_visits for the previous and current months.
func (me *PsqlDB) visitUrlFromRaw(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	where, with := visitFilterBy(opts)

	// Determine the effective start: max(origin, previousMonthStart)
	effectiveStart := previousMonthStart
	if opts.Origin.After(previousMonthStart) {
		effectiveStart = opts.Origin
	}

	topUrls := fmt.Sprintf(`
		SELECT
			path,
			count(DISTINCT ip_address) as path_count
		FROM analytics_visits
		WHERE created_at >= $1 AND created_at < $2 AND %s = $3 AND user_id = $4 AND path <> '' AND status <> 404
		GROUP BY path
		ORDER BY path_count DESC
		LIMIT 10`, where)

	rows, err := me.Db.Queryx(topUrls, effectiveStart, currentMonthStart.AddDate(0, 1, 0), with, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (me *PsqlDB) VisitUrlNotFound(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = 10
	}

	var historical, current []*db.VisitUrl
	var histErr, rawErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		historical, histErr = me.visitUrlNotFoundFromSummary(opts, limit)
	}()
	go func() {
		defer wg.Done()
		current, rawErr = me.visitUrlNotFoundFromRaw(opts, limit)
	}()

	wg.Wait()

	if histErr != nil {
		return nil, fmt.Errorf("query summary 404 urls: %w", histErr)
	}
	if rawErr != nil {
		return nil, fmt.Errorf("query raw 404 urls: %w", rawErr)
	}

	return mergeTopUrls(historical, current), nil
}

// visitUrlNotFoundFromSummary reads top 404 URLs from analytics_monthly_top_urls for historical data.
func (me *PsqlDB) visitUrlNotFoundFromSummary(opts *db.SummaryOpts, limit int) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	// If origin is in the previous month or later, raw data covers it — no summary to fetch.
	if !opts.Origin.Before(previousMonthStart) {
		return nil, nil
	}

	// Clamp origin to month boundary for summary table lookup
	originMonthStart := time.Date(opts.Origin.Year(), opts.Origin.Month(), 1, 0, 0, 0, 0, time.UTC)

	where := ""
	args := []interface{}{opts.UserID, originMonthStart, currentMonthStart}
	argIdx := 4
	if opts.Host != "" {
		where = "AND host = $" + fmt.Sprintf("%d", argIdx)
		args = append(args, opts.Host)
	}

	query := fmt.Sprintf(`
		SELECT path, sum(unique_visits) as total_visits
		FROM analytics_monthly_top_urls
		WHERE user_id = $1 AND month >= $2 AND month < $3 AND status_code = 404 %s
		GROUP BY path
		ORDER BY total_visits DESC
		LIMIT %d`, where, limit)

	rows, err := me.Db.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// visitUrlNotFoundFromRaw reads top 404 URLs from analytics_visits for the previous and current months.
func (me *PsqlDB) visitUrlNotFoundFromRaw(opts *db.SummaryOpts, limit int) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	where, with := visitFilterBy(opts)

	// Determine the effective start: max(origin, previousMonthStart)
	effectiveStart := previousMonthStart
	if opts.Origin.After(previousMonthStart) {
		effectiveStart = opts.Origin
	}

	topUrls := fmt.Sprintf(`
		SELECT
			path,
			count(DISTINCT ip_address) as path_count
		FROM analytics_visits
		WHERE created_at >= $1 AND created_at < $2 AND %s = $3 AND user_id = $4 AND path <> '' AND status = 404
		GROUP BY path
		ORDER BY path_count DESC
		LIMIT %d`, where, limit)

	rows, err := me.Db.Queryx(topUrls, effectiveStart, currentMonthStart.AddDate(0, 1, 0), with, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (me *PsqlDB) visitHost(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	var historical, current []*db.VisitUrl
	var histErr, rawErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		historical, histErr = me.visitHostFromSummary(opts)
	}()
	go func() {
		defer wg.Done()
		current, rawErr = me.visitHostFromRaw(opts)
	}()

	wg.Wait()

	if histErr != nil {
		return nil, fmt.Errorf("query summary hosts: %w", histErr)
	}
	if rawErr != nil {
		return nil, fmt.Errorf("query raw hosts: %w", rawErr)
	}

	return mergeHosts(historical, current), nil
}

// visitHostFromSummary reads host data from analytics_user_sites for historical data.
func (me *PsqlDB) visitHostFromSummary(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	rows, err := me.Db.Queryx(`
		SELECT host, total_visits
		FROM analytics_user_sites
		WHERE user_id = $1 AND host <> ''
		ORDER BY total_visits DESC`, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// visitHostFromRaw reads hosts from analytics_visits for the current month that aren't in summary.
func (me *PsqlDB) visitHostFromRaw(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	rows, err := me.Db.Queryx(`
		SELECT host, count(DISTINCT ip_address) as host_count
		FROM analytics_visits
		WHERE created_at >= $1 AND user_id = $2 AND host <> ''
		GROUP BY host
		ORDER BY host_count DESC`, currentMonthStart, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (me *PsqlDB) VisitSummary(opts *db.SummaryOpts) (*db.SummaryVisits, error) {
	var (
		visitors    []*db.VisitInterval
		urls        []*db.VisitUrl
		refs        []*db.VisitUrl
		notFound    []*db.VisitUrl
		visitorsErr error
		urlsErr     error
		refsErr     error
		nfErr       error
	)

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		visitors, visitorsErr = me.visitUnique(opts)
	}()
	go func() {
		defer wg.Done()
		urls, urlsErr = me.visitUrl(opts)
	}()
	go func() {
		defer wg.Done()
		refs, refsErr = me.visitReferer(opts)
	}()
	go func() {
		defer wg.Done()
		notFound, nfErr = me.VisitUrlNotFound(opts)
	}()

	wg.Wait()

	// Return the first error encountered
	for _, err := range []error{visitorsErr, urlsErr, refsErr, nfErr} {
		if err != nil {
			return nil, err
		}
	}

	return &db.SummaryVisits{
		Intervals:    visitors,
		TopUrls:      urls,
		TopReferers:  refs,
		NotFoundUrls: notFound,
	}, nil
}

func (me *PsqlDB) FindVisitSiteList(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	return me.visitHost(opts)
}
