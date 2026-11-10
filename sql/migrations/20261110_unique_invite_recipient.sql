BEGIN;

LOCK TABLE invites IN ACCESS EXCLUSIVE MODE;

DELETE FROM invites
WHERE id IN (
  SELECT id FROM (
    SELECT id, ROW_NUMBER() OVER (
      PARTITION BY to_user_id ORDER BY created_at, id
    ) AS position
    FROM invites
  ) ranked
  WHERE position > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS invites_to_user_id_key ON invites (to_user_id);

COMMIT;
