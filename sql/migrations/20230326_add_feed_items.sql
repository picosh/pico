CREATE TABLE IF NOT EXISTS feed_items (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  post_id uuid NOT NULL,
  guid character varying (1000) NOT NULL,
  data jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT feed_items_pkey PRIMARY KEY (id),
  CONSTRAINT fk_feed_items_posts
    FOREIGN KEY(post_id)
  REFERENCES posts(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
