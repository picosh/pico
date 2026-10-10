INSERT INTO feature_flags (user_id, name, expires_at)
SELECT DISTINCT ON (ff.user_id) ff.user_id, 'pastes', ff.expires_at
FROM feature_flags ff
JOIN invites ON invites.to_user_id = ff.user_id
WHERE ff.name = 'prose'
  AND NOT EXISTS (
    SELECT 1 FROM feature_flags existing
    WHERE existing.user_id = ff.user_id AND existing.name = 'pastes'
  )
ORDER BY ff.user_id, ff.expires_at DESC;
