BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- bcrypt reads only the first 72 bytes of a longer password.
UPDATE projects
SET acl = jsonb_set(
  acl, '{data}', jsonb_build_array(crypt(acl->'data'->>0, gen_salt('bf', 10)))
)
WHERE acl->>'type' = 'http-pass'
  AND jsonb_typeof(acl->'data'->0) = 'string'
  AND acl->'data'->>0 <> ''
  AND acl->'data'->>0 !~ '^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$';

COMMIT;
