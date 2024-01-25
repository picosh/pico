CREATE TABLE IF NOT EXISTS payment_history (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid,
  amount bigint NOT NULL,
  payment_type character varying(50),
  data jsonb NOT NULL DEFAULT '{"notes": ""}'::jsonb,
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT payment_history_aliases_pkey PRIMARY KEY (id),
  CONSTRAINT fk_payment_history_users
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
);

ALTER TABLE feature_flags DROP CONSTRAINT user_features_unique_name;
ALTER TABLE feature_flags ADD COLUMN expires_at timestamp without time zone
  NOT NULL DEFAULT NOW() + '1 year'::interval;
ALTER TABLE feature_flags ADD COLUMN data jsonb
  NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE feature_flags ADD COLUMN payment_history_id uuid;
ALTER TABLE feature_flags ADD CONSTRAINT fk_features_payment_history
    FOREIGN KEY(payment_history_id)
  REFERENCES payment_history(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE;
