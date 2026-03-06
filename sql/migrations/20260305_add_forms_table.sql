CREATE TABLE IF NOT EXISTS form_entries (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  name VARCHAR(255) NOT NULL,
  data jsonb NOT NULL,
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT form_entries_pkey PRIMARY KEY (id),
  CONSTRAINT fk_form_entries_users
    FOREIGN KEY(user_id)
    REFERENCES app_users(id)
    ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_form_entries_user ON form_entries(user_id);
CREATE INDEX IF NOT EXISTS idx_form_entries_name ON form_entries(name);