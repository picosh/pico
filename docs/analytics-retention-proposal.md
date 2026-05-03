# Analytics Data Retention Proposal

## Problem

The `analytics_visits` table has **18 million rows** in production. All analytics queries (unique visitors, top URLs, top referers, 404s) aggregate raw visit data on every request, causing slow query performance. The table continues to grow unbounded.

## Goal

- Keep `analytics_visits` small by deleting raw data older than **1 month**
- Pre-aggregate monthly stats into summary tables so historical queries remain fast
- Preserve all data the user currently sees in the TUI and SSH CLI

---

## What We Display Today

### TUI Analytics Screen (`pkg/tui/analytics.go`)

When a user views analytics for a site, we show:

| Section | Query | Aggregation |
|---------|-------|-------------|
| **Site list** (left pane) | `visitHost()` | `COUNT(DISTINCT ip_address)` grouped by `host` |
| **Visits by period** | `visitUnique()` | `COUNT(DISTINCT ip_address)` grouped by `date_trunc(interval, created_at)` |
| **Top URLs** | `visitUrl()` | `COUNT(DISTINCT ip_address)` grouped by `path`, LIMIT 10, excludes 404s |
| **Top Referers** | `visitReferer()` | `COUNT(DISTINCT ip_address)` grouped by `referer`, LIMIT 10, excludes 404s |
| **Top 404s** | `VisitUrlNotFound()` | `COUNT(DISTINCT ip_address)` grouped by `path`, LIMIT 10, status=404 |

The TUI supports two intervals:
- **"day"** — daily buckets from start of current month (`StartOfMonth()`)
- **"month"** — monthly buckets from 1 year ago (`StartOfYear()` which is `now - 1 year`)

### SSH CLI (`pkg/apps/pico/cli.go`)

| Command | Query | Aggregation |
|---------|-------|-------------|
| `not-found hostname.com [year\|month]` | `VisitUrlNotFound()` | `COUNT(DISTINCT ip_address)` grouped by `path`, LIMIT 100, status=404 |

---

## Proposed Schema

### New Table: `analytics_user_sites`

Tracks every site a user has had traffic on. Powers the site list in the left pane of the TUI.

```sql
CREATE TABLE analytics_user_sites (
    id              SERIAL PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    host            VARCHAR(253) NOT NULL,
    total_visits    INT NOT NULL DEFAULT 0,       -- running total of all-time unique visitors
    last_seen       DATE NOT NULL,                 -- most recent month with traffic
    created_at      TIMESTAMP NOT NULL DEFAULT now(),
    updated_at      TIMESTAMP NOT NULL DEFAULT now(),

    UNIQUE (user_id, host)
);

CREATE INDEX idx_user_sites_user ON analytics_user_sites (user_id);
```

**How it's populated:** During monthly aggregation, for each `(user_id, host)` pair found in the previous month's raw data:
- `INSERT ... ON CONFLICT (user_id, host) DO UPDATE` — add that month's unique visitor count to `total_visits`, set `last_seen` to the month's date.

**Query replacement:** `visitHost()` becomes:
```sql
SELECT host, total_visits as host_count
FROM analytics_user_sites
WHERE user_id = $1
ORDER BY total_visits DESC
```

No GROUP BY, no scan of raw data — just an indexed lookup.

### New Table: `analytics_monthly_visits`

Stores pre-aggregated daily unique visitor counts per host. This powers the "visits by period" chart and site list.

```sql
CREATE TABLE analytics_monthly_visits (
    id              SERIAL PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    host            VARCHAR(253) NOT NULL,
    visit_date      DATE NOT NULL,              -- the day this count is for
    unique_visits   INT NOT NULL DEFAULT 0,     -- COUNT(DISTINCT ip_address) for that day
    mobile_visits   INT NOT NULL DEFAULT 0,     -- unique visitors from mobile user-agents
    desktop_visits  INT NOT NULL DEFAULT 0,     -- unique visitors from desktop user-agents
    created_at      TIMESTAMP NOT NULL DEFAULT now(),

    UNIQUE (user_id, host, visit_date)
);

CREATE INDEX idx_monthly_visits_user_host ON analytics_monthly_visits (user_id, host);
CREATE INDEX idx_monthly_visits_user_date ON analytics_monthly_visits (user_id, visit_date DESC);
```

**Why this shape:** The TUI queries `date_trunc('day', created_at)` or `date_trunc('month', created_at)` with `COUNT(DISTINCT ip_address)`. We pre-compute the daily distinct count. For "month" interval view, the app sums daily rows within each month client-side or via a simple `GROUP BY date_trunc('month', visit_date)`.

**Device detection:** `mobile_visits` and `desktop_visits` are derived from the `user_agent` column during aggregation. A user-agent library (e.g., [`pdericx/ua-parser`](https://github.com/pdericx/ua-parser) or [`mssola/useragent`](https://github.com/mssola/useragent)) classifies each visit. An IP counted as mobile if its user-agent is mobile, desktop otherwise. Note: `mobile_visits + desktop_visits = unique_visits` since every visit is one or the other.

### New Table: `analytics_monthly_top_urls`

Stores the top URLs per host per month. Powers "Top URLs" and "Top 404s" sections.

```sql
CREATE TABLE analytics_monthly_top_urls (
    id            SERIAL PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    host          VARCHAR(253) NOT NULL,
    month         DATE NOT NULL,           -- first day of the month (e.g., 2025-01-01)
    path          VARCHAR(2048) NOT NULL,
    unique_visits INT NOT NULL DEFAULT 0,  -- COUNT(DISTINCT ip_address) for that path
    is_404        BOOLEAN NOT NULL DEFAULT false,
    rank          INT NOT NULL,            -- 1-10 (top 10) or 1-100 for CLI
    created_at    TIMESTAMP NOT NULL DEFAULT now(),

    UNIQUE (user_id, host, month, path, is_404)
);

CREATE INDEX idx_monthly_top_urls_user_host_month ON analytics_monthly_top_urls (user_id, host, month);
CREATE INDEX idx_monthly_top_urls_404 ON analytics_monthly_top_urls (user_id, host, month, is_404, rank);
```

### New Table: `analytics_monthly_top_referers`

Stores top referers per host per month. Powers "Top Referers" section.

```sql
CREATE TABLE analytics_monthly_top_referers (
    id            SERIAL PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    host          VARCHAR(253) NOT NULL,
    month         DATE NOT NULL,           -- first day of the month
    referer       VARCHAR(253) NOT NULL,
    unique_visits INT NOT NULL DEFAULT 0,
    rank          INT NOT NULL,            -- 1-10
    created_at    TIMESTAMP NOT NULL DEFAULT now(),

    UNIQUE (user_id, host, month, referer)
);

CREATE INDEX idx_monthly_referers_user_host_month ON analytics_monthly_top_referers (user_id, host, month);
```

---

## Monthly Aggregation Job

A script (e.g., `cmd/scripts/analytics-aggregate/main.go`) runs **once per month** (e.g., on the 1st). It:

### Step 1: Aggregate the previous month

For each user with the `analytics` feature flag who has raw visits in the previous month:

```
FOR each (user_id, host) pair in analytics_visits WHERE created_at IN previous_month:
    -- Daily unique visitors (with device breakdown)
    -- Device detection runs in Go (user-agent parsing), not pure SQL.
    -- Pseudo-SQL for clarity:
    INSERT INTO analytics_monthly_visits (user_id, host, visit_date, unique_visits, mobile_visits, desktop_visits)
    SELECT user_id, host, date_trunc('day', created_at)::date,
           COUNT(DISTINCT ip_address),
           COUNT(DISTINCT CASE WHEN is_mobile(user_agent) THEN ip_address END),
           COUNT(DISTINCT CASE WHEN NOT is_mobile(user_agent) THEN ip_address END)
    FROM analytics_visits
    WHERE user_id = $1 AND host = $2 AND created_at >= start_of_month AND created_at < start_of_next_month
    GROUP BY user_id, host, date_trunc('day', created_at)
    ON CONFLICT (user_id, host, visit_date) DO NOTHING;

    -- In practice, `is_mobile()` is a Go function. The aggregation job fetches
    -- raw rows in batches, classifies each user_agent, and issues INSERTs.

    -- Top 10 URLs (non-404)
    INSERT INTO analytics_monthly_top_urls (user_id, host, month, path, unique_visits, is_404, rank)
    SELECT user_id, host, date_trunc('month', created_at)::date, path, COUNT(DISTINCT ip_address), false,
           ROW_NUMBER() OVER (PARTITION BY user_id, host ORDER BY COUNT(DISTINCT ip_address) DESC)
    FROM analytics_visits
    WHERE user_id = $1 AND host = $2 AND created_at >= start_of_month AND created_at < start_of_next_month AND status <> 404
    GROUP BY user_id, host, path
    HAVING ROW_NUMBER() <= 10
    ON CONFLICT (user_id, host, month, path, is_404) DO NOTHING;

    -- Top 10 URLs (404s)
    INSERT INTO analytics_monthly_top_urls (user_id, host, month, path, unique_visits, is_404, rank)
    SELECT user_id, host, date_trunc('month', created_at)::date, path, COUNT(DISTINCT ip_address), true,
           ROW_NUMBER() OVER (PARTITION BY user_id, host ORDER BY COUNT(DISTINCT ip_address) DESC)
    FROM analytics_visits
    WHERE user_id = $1 AND host = $2 AND created_at >= start_of_month AND created_at < start_of_next_month AND status = 404
    GROUP BY user_id, host, path
    HAVING ROW_NUMBER() <= 100   -- 100 for CLI, 10 for TUI (use 100 to cover both)
    ON CONFLICT (user_id, host, month, path, is_404) DO NOTHING;

    -- Top 10 referers
    INSERT INTO analytics_monthly_top_referers (user_id, host, month, referer, unique_visits, rank)
    SELECT user_id, host, date_trunc('month', created_at)::date, referer, COUNT(DISTINCT ip_address),
           ROW_NUMBER() OVER (PARTITION BY user_id, host ORDER BY COUNT(DISTINCT ip_address) DESC)
    FROM analytics_visits
    WHERE user_id = $1 AND host = $2 AND created_at >= start_of_month AND created_at < start_of_next_month AND referer <> '' AND status <> 404
    GROUP BY user_id, host, referer
    HAVING ROW_NUMBER() <= 10
    ON CONFLICT (user_id, host, month, referer) DO NOTHING;
```

### Step 2: Delete raw data older than 1 month

```sql
DELETE FROM analytics_visits
WHERE created_at < date_trunc('month', now())::timestamp;
```

This keeps only the current month's raw data. After the first run, `analytics_visits` should drop from ~18M rows to ~1 month of data (estimated 150K–500K depending on traffic).

### Step 3: Backfill (one-time)

On first deployment, run the aggregation for all historical months before deleting anything. This is a separate migration pass that iterates month-by-month from the oldest data to 2 months ago.

---

## Query Changes

### Current queries → New queries

Each existing query method gets a `since` parameter. If `since` is within the last month, query `analytics_visits` directly (current behavior). If `since` spans older months, union results from summary tables with raw data.

#### `visitUnique()` — visits by period

```
-- For "day" interval (current month only, always from raw):
-- No change — reads from analytics_visits

-- For "month" interval (historical):
-- Read from analytics_monthly_visits, sum daily counts per month:
SELECT visit_date::date as interval_start, SUM(unique_visits) as visitors
FROM analytics_monthly_visits
WHERE user_id = $1 AND host = $2 AND visit_date >= $3
GROUP BY date_trunc('month', visit_date), visit_date
ORDER BY visit_date
```

#### `visitUrl()` — top URLs

```
-- Current month: read from analytics_visits (no change)
-- Historical months: read from analytics_monthly_top_urls WHERE is_404 = false
```

#### `visitReferer()` — top referers

```
-- Current month: read from analytics_visits (no change)  
-- Historical months: read from analytics_monthly_top_referers
```

#### `VisitUrlNotFound()` — top 404s

```
-- Current month: read from analytics_visits (no change)
-- Historical months: read from analytics_monthly_top_urls WHERE is_404 = true
```

#### `visitHost()` — site list

```
-- Replaced entirely by analytics_user_sites lookup:
SELECT host, total_visits as host_count
FROM analytics_user_sites
WHERE user_id = $1
ORDER BY total_visits DESC
```

No raw table scan. Current month's new hosts are upserted during the monthly aggregation job.

---

## Implementation Phases

### Phase 1: Schema + Backfill (no behavior change)
1. Create the four new tables (user_sites, monthly_visits, monthly_top_urls, monthly_top_referers)
2. Write the aggregation script (`cmd/scripts/analytics-aggregate/`)
3. Run backfill for all historical data
4. **Do not delete any data yet** — verify summary tables are correct

### Phase 2: Query migration
1. Add new DB interface methods that read from summary tables
2. Modify existing query methods to union summary + raw data
3. Verify TUI and CLI output matches before/after
4. Run `make check`

### Phase 3: Enable deletion
1. Add raw data deletion to the aggregation job
2. Schedule the job to run monthly (cron, systemd timer, or app-level scheduler)
3. Monitor `analytics_visits` row count after first deletion

### Phase 4: Cleanup
1. Remove any code paths that no longer need raw historical data
2. Consider dropping unused indexes on `analytics_visits` since the table is now small

---

## Trade-offs

| Aspect | Before | After |
|--------|--------|-------|
| `analytics_visits` size | 18M+ rows (unbounded) | ~1 month of data (~150K-500K) |
| Query latency | Scans full table each time | Reads small table or pre-aggregated rows |
| Historical granularity | Full raw data | Monthly top-10/100 summaries |
| Disk overhead | Single table | 4 additional tables (user_sites, monthly_visits, monthly_top_urls, monthly_top_referers) |
| Mobile/desktop | Not tracked | Tracked via user-agent classification during aggregation |
| Data loss | None | Raw data older than 1 month is gone |

The key trade-off is that we lose the ability to run *arbitrary* ad-hoc queries on historical data. But we preserve exactly what the user sees today in the TUI and CLI. If we need new historical queries in the future, we'd add them to the aggregation job.

---

## Open Questions

1. **Scheduling**: How do we run the monthly job? Options:
   - Cron job on the server
   - `systemd` timer
   - Built into one of our services as a startup check
   - External scheduler (GitHub Actions, etc.)

2. **`visitHost()` (site list)**: Solved by `analytics_user_sites` table — simple indexed lookup, no aggregation needed.

3. **Retention period**: Is 1 month the right window for raw data? Could adjust based on observed query patterns.

4. **Concurrent writes**: During the aggregation window, new visits are still being inserted. The job needs to handle this (e.g., aggregate up to a specific timestamp, then delete up to that same timestamp).

5. **Users who disable analytics**: The banner says "when analytics are disabled we do not purge usage statistics." Deletion should respect this — only delete for users with the `analytics` feature flag active.

6. **Device detection library**: Which Go user-agent parser to use for mobile/desktop classification? Candidates:
   - [`mssola/useragent`](https://github.com/mssola/useragent) — lightweight, device.OS / device.Type
   - [`pdericx/ua-parser`](https://github.com/pdericx/ua-parser) — fuller UAParser port
   - [`danielgtaylor/mobile`](https://github.com/danielgtaylor/mobile) — simple mobile-only detection
   The existing codebase already uses `x-way/crawlerdetect` for bot filtering, so a similar pattern fits.
