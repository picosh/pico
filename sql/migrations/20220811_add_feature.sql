CREATE TABLE IF NOT EXISTS feature_flags (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  name character varying(50),
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT user_features_unique_name UNIQUE (user_id, name),
  CONSTRAINT feature_flags_pkey PRIMARY KEY (id),
  CONSTRAINT fk_features_user_post
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
