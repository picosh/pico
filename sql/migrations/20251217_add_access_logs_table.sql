CREATE TABLE IF NOT EXISTS access_logs (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  service character varying(255) NOT NULL,
  pubkey text NOT NULL DEFAULT '',
  identity text NOT NULL DEFAULT '',
  data jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT access_logs_pkey PRIMARY KEY (id),
  CONSTRAINT fk_access_logs_app_users
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
