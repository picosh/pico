CREATE TABLE IF NOT EXISTS analytics_visits (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  project_id uuid,
  post_id uuid,
  host varchar(253),
  path varchar(2048),
  ip_address varchar(46),
  user_agent varchar(1000),
  referer varchar(253),
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT analytics_visits_pkey PRIMARY KEY (id),
  CONSTRAINT fk_visits_user
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE,
  CONSTRAINT fk_visits_project
    FOREIGN KEY(project_id)
  REFERENCES projects(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE,
  CONSTRAINT fk_visits_post
    FOREIGN KEY(post_id)
  REFERENCES posts(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
