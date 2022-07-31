ALTER TABLE posts ADD COLUMN slug character varying(255) NOT NULL DEFAULT '';
ALTER TABLE posts ADD CONSTRAINT unique_slug_for_user UNIQUE (user_id, cur_space, slug);
UPDATE posts SET slug = filename;
