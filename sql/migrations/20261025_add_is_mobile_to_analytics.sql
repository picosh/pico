ALTER TABLE analytics_visits ADD COLUMN IF NOT EXISTS is_mobile boolean;

-- Only the current and previous month are read from raw visits. Matches db.IsMobileUserAgent.
UPDATE analytics_visits
SET is_mobile = (
  COALESCE(user_agent, '') ILIKE '%mobile%'
  OR COALESCE(user_agent, '') ILIKE '%android%'
  OR COALESCE(user_agent, '') ILIKE '%iphone%'
  OR COALESCE(user_agent, '') ILIKE '%ipad%'
  OR COALESCE(user_agent, '') ILIKE '%ipod%'
  OR COALESCE(user_agent, '') ILIKE '%blackberry%'
  OR COALESCE(user_agent, '') ILIKE '%windows phone%'
)
WHERE is_mobile IS NULL
  AND created_at >= date_trunc('month', now() AT TIME ZONE 'UTC') - interval '1 month';
