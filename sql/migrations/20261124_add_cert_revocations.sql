CREATE TABLE IF NOT EXISTS cert_revocations (
  user_id uuid NOT NULL,
  kind character varying(3) NOT NULL,
  value text NOT NULL,
  note text NOT NULL DEFAULT '',
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT cert_revocations_pkey PRIMARY KEY (user_id, kind, value),
  CONSTRAINT cert_revocations_kind CHECK (kind IN ('key', 'id')),
  CONSTRAINT cert_revocations_value CHECK (value <> ''),
  CONSTRAINT fk_cert_revocations_app_users
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
