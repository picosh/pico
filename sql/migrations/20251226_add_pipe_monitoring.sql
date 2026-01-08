CREATE TABLE IF NOT EXISTS pipe_monitors (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  topic text NOT NULL,
  window_dur interval NOT NULL,
  window_end timestamp without time zone NOT NULL DEFAULT NOW(),
  last_ping timestamp,
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  updated_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT pipe_monitors_unique_topic UNIQUE (user_id, topic),
  CONSTRAINT pipe_monitoring_pkey PRIMARY KEY (id),
  CONSTRAINT fk_pipe_monitoring_app_users
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);

CREATE TABLE IF NOT EXISTS pipe_monitors_history (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  monitor_id uuid NOT NULL,
  window_dur interval NOT NULL,
  window_end timestamp without time zone NOT NULL DEFAULT NOW(),
  last_ping timestamp,
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  updated_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT pipe_monitor_history_pkey PRIMARY KEY (id),
  CONSTRAINT fk_pipe_monitor_history_pipe_monitors
    FOREIGN KEY(monitor_id)
  REFERENCES pipe_monitors(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_pipe_mon_hist_monitor_last_ping ON pipe_monitors_history (monitor_id, last_ping);
CREATE INDEX IF NOT EXISTS idx_pipe_mon_hist_monitor_window_end ON pipe_monitors_history (monitor_id, window_end);
