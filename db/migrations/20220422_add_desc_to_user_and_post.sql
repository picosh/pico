ALTER TABLE app_users ADD COLUMN bio character varying(150) NOT NULL DEFAULT '';
ALTER TABLE posts ADD COLUMN description character varying(150) NOT NULL DEFAULT '';
ALTER TABLE posts ADD COLUMN filename character varying(255);

UPDATE posts SET filename = title;

ALTER TABLE posts ADD CONSTRAINT unique_filename_for_user UNIQUE (user_id, filename);
ALTER TABLE posts DROP CONSTRAINT unique_title_for_user;
