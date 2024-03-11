ALTER TABLE public_keys ADD COLUMN name varchar(256) NOT NULL DEFAULT '';
-- cannot add this because all names are empty
-- ALTER TABLE public_keys ADD CONSTRAINT unique_public_keys_user_name UNIQUE (user_id, name);
