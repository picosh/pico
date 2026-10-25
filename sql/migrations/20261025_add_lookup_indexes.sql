CREATE INDEX IF NOT EXISTS feed_items_post_idx ON feed_items (post_id);
CREATE INDEX IF NOT EXISTS tuns_event_logs_user_created_idx ON tuns_event_logs (user_id, created_at);
CREATE INDEX IF NOT EXISTS feature_flags_user_name_idx ON feature_flags (user_id, name);
CREATE INDEX IF NOT EXISTS public_keys_public_key_idx ON public_keys (public_key);
CREATE INDEX IF NOT EXISTS post_aliases_slug_idx ON post_aliases (slug);
CREATE INDEX IF NOT EXISTS post_aliases_post_idx ON post_aliases (post_id);
