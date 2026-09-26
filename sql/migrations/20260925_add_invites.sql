CREATE TABLE IF NOT EXISTS invites (
  id SERIAL PRIMARY KEY,
  from_user_id uuid NOT NULL,
  to_user_id uuid NOT NULL,
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT fk_invites_from_app_users
    FOREIGN KEY(from_user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE,
  CONSTRAINT fk_invites_to_app_users
    FOREIGN KEY(to_user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
