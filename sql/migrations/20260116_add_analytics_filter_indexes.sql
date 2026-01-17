CREATE INDEX analytics_visits_user_created_idx 
  ON analytics_visits(user_id, created_at DESC);

CREATE INDEX analytics_visits_user_host_created_status_idx 
  ON analytics_visits(user_id, host, created_at DESC, status);

CREATE INDEX analytics_visits_user_path_created_status_idx 
  ON analytics_visits(user_id, path, created_at DESC, status);
