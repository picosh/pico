-- find unused accounts
SELECT count(*) FROM app_users u WHERE NOT EXISTS (SELECT 1 FROM posts WHERE user_id = u.id) AND NOT EXISTS (SELECT 1 FROM projects WHERE user_id = u.id) AND NOT EXISTS (SELECT 1 FROM access_logs WHERE user_id = u.id AND created_at > NOW() - INTERVAL '1 year') AND NOT EXISTS (SELECT 1 FROM feature_flags ff WHERE ff.user_id = u.id) AND NOT EXISTS (SELECT 1 FROM payment_history ph WHERE ph.user_id = u.id);
-- delete unused accounts
DELETE FROM app_users u WHERE NOT EXISTS (SELECT 1 FROM posts WHERE user_id = u.id) AND NOT EXISTS (SELECT 1 FROM projects WHERE user_id = u.id) AND NOT EXISTS (SELECT 1 FROM access_logs WHERE user_id = u.id AND created_at > NOW() - INTERVAL '1 year') AND NOT EXISTS (SELECT 1 FROM feature_flags ff WHERE ff.user_id = u.id) AND NOT EXISTS (SELECT 1 FROM payment_history ph WHERE ph.user_id = u.id);

-- how many visits will be deleted
SELECT count(*) FROM analytics_visits WHERE created_at < NOW() - INTERVAL '1 year';
-- delete old analytic visits
DELETE FROM analytics_visits WHERE created_at < NOW() - INTERVAL '1 year';
-- batch delete
WITH deleted AS (DELETE FROM analytics_visits WHERE ctid IN (SELECT ctid FROM analytics_visits WHERE created_at < NOW() - INTERVAL '1 year' LIMIT 100000) RETURNING 1) SELECT count(*) FROM deleted;
