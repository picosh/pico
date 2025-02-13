CREATE EXTENSION IF NOT EXISTS "ltree";

CREATE TABLE IF NOT EXISTS link_tree (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  short_id character varying(10),
  path ltree,
  text text NOT NULL DEFAULT '',
  title character varying(255),
  url text,
  img_url text,
  perm character varying(10) NOT NULL DEFAULT 'write',
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  updated_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT link_tree_pkey PRIMARY KEY (id),
  CONSTRAINT fk_user_link_tree_owner
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE,
  CONSTRAINT unique_short_id_for_link_tree UNIQUE (short_id)
);

CREATE TABLE IF NOT EXISTS mods (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  link_id uuid NOT NULL,
  perm character varying(10) NOT NULL DEFAULT 'write',
  reason text NOT NULL DEFAULT '',
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT mods_pkey PRIMARY KEY (id),
  CONSTRAINT fk_user_mods_owner
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE,
  CONSTRAINT fk_link_tree_mods
    FOREIGN KEY(link_id)
  REFERENCES link_tree(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE,
  CONSTRAINT unique_mod_for_user_link_tree UNIQUE (user_id, link_id)
);

CREATE TABLE IF NOT EXISTS votes (
  id uuid NOT NULL DEFAULT uuid_generate_v4(),
  user_id uuid NOT NULL,
  link_id uuid NOT NULL,
  created_at timestamp without time zone NOT NULL DEFAULT NOW(),
  CONSTRAINT votes_pkey PRIMARY KEY (id),
  CONSTRAINT fk_user_votes_owner
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE,
  CONSTRAINT fk_link_tree_votes
    FOREIGN KEY(link_id)
  REFERENCES link_tree(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE,
  CONSTRAINT unique_vote_for_user_link_tree UNIQUE (user_id, link_id)
);
