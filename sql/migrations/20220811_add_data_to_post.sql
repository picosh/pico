ALTER TABLE posts ADD COLUMN shasum char(64);
ALTER TABLE posts ADD COLUMN mime_type char(32);
ALTER TABLE posts ADD COLUMN file_size int NOT NULL DEFAULT 0;
ALTER TABLE posts ADD COLUMN data jsonb NOT NULL DEFAULT '{}'::jsonb;
