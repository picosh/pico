CREATE TABLE IF NOT EXISTS tuns_event_logs (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  server_id text NOT NULL,
  remote_addr text NOT NULL,
  event_type text NOT NULL,
  tunnel_type text NOT NULL,
  connection_type text NOT NULL,
  tunnel_addrs text[] NOT NULL,
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT tuns_event_logs_pkey PRIMARY KEY (id),
  CONSTRAINT fk_tuns_event_logs_user
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
