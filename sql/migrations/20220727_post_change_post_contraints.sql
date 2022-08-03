CREATE TYPE space AS ENUM ('prose', 'lists', 'pastes', 'imgs');

ALTER TABLE posts ADD COLUMN cur_space space;
ALTER TABLE posts ADD COLUMN views int DEFAULT 0;
ALTER TABLE posts DROP CONSTRAINT unique_filename_for_user;
ALTER TABLE posts ADD CONSTRAINT unique_filename_for_user UNIQUE (user_id, cur_space, filename);

DROP TABLE post_analytics;
