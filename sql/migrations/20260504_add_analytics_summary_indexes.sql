-- Composite index for visitUniqueFromSummary: filters by (user_id, host, visit_date)
CREATE INDEX IF NOT EXISTS idx_monthly_visits_user_host_date
ON analytics_monthly_visits (user_id, host, visit_date);

-- Covering index for visitHostFromRaw: filters by (user_id, created_at), groups by host, counts distinct ip_address
CREATE INDEX IF NOT EXISTS idx_analytics_visits_user_created_host_ip
ON analytics_visits (user_id, created_at, host, ip_address);
