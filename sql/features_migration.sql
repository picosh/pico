UPDATE feature_flags SET name = 'plus' WHERE name = 'pgs';
UPDATE feature_flags SET data = '{"file_max": 50000000, "storage_max": 20000000000}'::jsonb WHERE name = 'plus';

-- DELETE FROM feature_flags WHERE name = 'imgs' OR name = 'prose' OR name = 'tuns';
