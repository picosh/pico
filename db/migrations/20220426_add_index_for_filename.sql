CREATE INDEX posts_filename ON posts USING btree(filename);
ALTER TABLE app_users DROP COLUMN bio;
