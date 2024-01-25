-- find user id
SELECT id FROM app_users WHERE name = '{user}';

-- add payment record
-- amount should be multiplied by 1 million and then later divded by the same
-- https://stackoverflow.com/a/51238749
INSERT INTO payment_history (user_id, payment_type, amount, data)
VALUES ('', 'stripe', 20 * 1000000, '{"notes": ""}'::jsonb) RETURNING id;

-- enable pro features

-- pgs
-- storage max is 10gb
-- file max is 50mb
INSERT INTO feature_flags (user_id, name, data, expires_at)
VALUES ('', 'pgs', '{"storage_max":10737418240, "file_max":52428800}'::jsonb, now() + '1 year'::interval);

-- imgs
-- storage max is 10gb
-- file max is 50mb
INSERT INTO feature_flags (user_id, name, data, expires_at)
VALUES ('', 'imgs', '{"storage_max":10737418240, "file_max":52428800}'::jsonb, now() + '1 year'::interval);

-- tuns
INSERT INTO feature_flags (user_id, name, expires_at)
VALUES ('', 'tuns', now() + '1 year'::interval);
