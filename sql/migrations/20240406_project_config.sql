ALTER TABLE projects ADD COLUMN config jsonb NOT NULL default '{"headers":[],"redirects":[],"denylist":["\\/\\..+"]}';
