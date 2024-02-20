-- type could be: "public", "public_keys", "pico"
ALTER TABLE projects ADD COLUMN acl jsonb NOT NULL DEFAULT '{"type":"public","data":[]}'::jsonb;
