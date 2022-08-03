CREATE TABLE IF NOT EXISTS post_analytics (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  post_id uuid NOT NULL,
  views int DEFAULT 0,
  updated_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT analytics_pkey PRIMARY KEY (id),
  CONSTRAINT fk_analytics_posts
    FOREIGN KEY(post_id)
  REFERENCES posts(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
