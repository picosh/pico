CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS app_users (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  name character varying(50),
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT unique_name UNIQUE (name),
  CONSTRAINT app_user_pkey PRIMARY KEY (id)
);

CREATE TABLE IF NOT EXISTS public_keys (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  public_key varchar(2048) NOT NULL,
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT user_public_keys_pkey PRIMARY KEY (id),
  CONSTRAINT unique_key_for_user UNIQUE (user_id, public_key),
  CONSTRAINT fk_user_public_keys_owner
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);

CREATE TABLE IF NOT EXISTS posts (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  title character varying(255) NOT NULL,
  text text NOT NULL DEFAULT '',
  publish_at timestamp without time zone NOT NULL DEFAULT NOW(),
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  updated_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT posts_pkey PRIMARY KEY (id),
  CONSTRAINT unique_title_for_user UNIQUE (user_id, title),
  CONSTRAINT fk_posts_app_users
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);
