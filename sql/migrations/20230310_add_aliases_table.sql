CREATE TABLE IF NOT EXISTS post_aliases (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  post_id uuid NOT NULL,
  slug character varying(255) NOT NULL,
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT post_aliases_pkey PRIMARY KEY (id),
  CONSTRAINT fk_post_aliases_posts
    FOREIGN KEY(post_id)
  REFERENCES posts(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
