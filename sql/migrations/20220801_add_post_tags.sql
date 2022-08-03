CREATE TABLE IF NOT EXISTS post_tags (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  post_id uuid NOT NULL,
  name character varying(50),
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT post_tags_unique_name UNIQUE (post_id, name),
  CONSTRAINT post_tags_pkey PRIMARY KEY (id),
  CONSTRAINT fk_post_tags_post
    FOREIGN KEY(post_id)
  REFERENCES posts(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
