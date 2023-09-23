CREATE TABLE IF NOT EXISTS tokens (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  name varchar(256) NOT NULL,
  token varchar(256) NOT NULL DEFAULT uuid_generate_v4(),
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  expires_at timestamp without time zone NOT NULL DEFAULT '2100-01-01 00:00:00',
  CONSTRAINT user_tokens_pkey PRIMARY KEY (id),
  CONSTRAINT unique_token UNIQUE (token),
  CONSTRAINT unique_user_name UNIQUE (user_id, name),
  CONSTRAINT fk_user_tokens_owner
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
