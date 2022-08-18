ALTER TABLE posts ADD COLUMN shasum char(64) NOT NULL DEFAULT '';
ALTER TABLE posts ADD COLUMN mime_type character varying(250) NOT NULL DEFAULT '';
ALTER TABLE posts ADD COLUMN file_size int NOT NULL DEFAULT 0;
ALTER TABLE posts ADD COLUMN data jsonb NOT NULL DEFAULT '{}'::jsonb;
