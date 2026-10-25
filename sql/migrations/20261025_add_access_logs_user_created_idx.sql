CREATE INDEX CONCURRENTLY IF NOT EXISTS access_logs_user_created_idx ON access_logs (user_id, created_at);
