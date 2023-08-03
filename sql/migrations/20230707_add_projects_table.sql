CREATE TABLE IF NOT EXISTS projects (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  name character varying(255) NOT NULL,
  project_dir text NOT NULL DEFAULT '',
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  updated_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT projects_pkey PRIMARY KEY (id),
  CONSTRAINT unique_name_for_user UNIQUE (user_id, name),
  CONSTRAINT fk_projects_app_users
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
