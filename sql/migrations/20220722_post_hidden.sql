ALTER TABLE posts ADD COLUMN hidden boolean NOT NULL DEFAULT FALSE;
UPDATE posts SET hidden = TRUE WHERE filename LIKE E'\\_%';
