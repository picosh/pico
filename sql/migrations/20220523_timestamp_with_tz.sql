ALTER TABLE posts ALTER COLUMN updated_at TYPE timestamp WITH TIME ZONE USING updated_at AT TIME ZONE 'UTC';
ALTER TABLE posts ALTER COLUMN publish_at TYPE timestamp WITH TIME ZONE USING publish_at AT TIME ZONE 'UTC';
ALTER TABLE posts ALTER COLUMN created_at TYPE timestamp WITH TIME ZONE USING created_at AT TIME ZONE 'UTC';
ALTER TABLE app_users ALTER COLUMN created_at TYPE timestamp WITH TIME ZONE USING created_at AT TIME ZONE 'UTC';
ALTER TABLE public_keys ALTER COLUMN created_at TYPE timestamp WITH TIME ZONE USING created_at AT TIME ZONE 'UTC';
