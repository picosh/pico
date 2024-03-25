CREATE TABLE IF NOT EXISTS analytics_visits (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  project_id uuid,
  post_id uuid,
  url varchar(1000) NOT NULL DEFAULT uuid_generate_v4(),
  ip_address varchar(46) NOT NULL DEFAULT uuid_generate_v4(),
  user_agent varchar(1000) NOT NULL DEFAULT uuid_generate_v4(),
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT analytics_visits_pkey PRIMARY KEY (id),
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
