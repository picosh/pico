ALTER TABLE posts ADD COLUMN slug character varying(255) NOT NULL DEFAULT '';
UPDATE posts SET slug = filename;
UPDATE posts SET filename = '_styles.css' WHERE filename = '_styles' AND cur_space = 'prose';
UPDATE posts SET filename = filename || '.md' WHERE filename <> '_styles.css' AND cur_space = 'prose';
UPDATE posts SET filename = filename || '.txt' WHERE cur_space = 'lists';
ALTER TABLE posts ADD CONSTRAINT unique_slug_for_user UNIQUE (user_id, cur_space, slug);
