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
