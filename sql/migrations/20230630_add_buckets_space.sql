ALTER TYPE space ADD VALUE 'buckets';
ALTER TABLE posts ADD path character varying(255);

ALTER TABLE posts ADD CONSTRAINT unique_path_filename_for_user UNIQUE (user_id, cur_space, path, filename);
ALTER TABLE posts DROP CONSTRAINT unique_slug_for_user;
