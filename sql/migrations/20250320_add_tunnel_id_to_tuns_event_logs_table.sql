DELETE FROM tuns_event_logs;
ALTER TABLE tuns_event_logs ADD COLUMN tunnel_id text NOT NULL;
ALTER TABLE tuns_event_logs DROP COLUMN tunnel_addrs;