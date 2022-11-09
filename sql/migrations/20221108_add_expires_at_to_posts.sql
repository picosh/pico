ALTER TABLE posts ADD expires_at timestamp without time zone;

UPDATE posts SET expires_at = NOW() + INTERVAL '7 day' WHERE cur_space='pastes';
