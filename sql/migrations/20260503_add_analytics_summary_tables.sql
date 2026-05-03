-- analytics_user_sites: tracks every site a user has had traffic on
CREATE TABLE IF NOT EXISTS analytics_user_sites (
  id serial NOT NULL,
  user_id uuid NOT NULL,
  host character varying(253) NOT NULL,
  total_visits integer NOT NULL DEFAULT 0,
  last_seen date NOT NULL,
  created_at timestamp without time zone NOT NULL DEFAULT now(),
  updated_at timestamp without time zone NOT NULL DEFAULT now(),
  CONSTRAINT analytics_user_sites_pkey PRIMARY KEY (id),
  CONSTRAINT analytics_user_sites_unique UNIQUE (user_id, host),
  CONSTRAINT fk_user_sites_app_users
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_sites_user ON analytics_user_sites (user_id);

-- analytics_monthly_visits: daily unique visitor counts per host with device breakdown
CREATE TABLE IF NOT EXISTS analytics_monthly_visits (
  id serial NOT NULL,
  user_id uuid NOT NULL,
  host character varying(253) NOT NULL,
  visit_date date NOT NULL,
  unique_visits integer NOT NULL DEFAULT 0,
  mobile_visits integer NOT NULL DEFAULT 0,
  desktop_visits integer NOT NULL DEFAULT 0,
  created_at timestamp without time zone NOT NULL DEFAULT now(),
  CONSTRAINT analytics_monthly_visits_pkey PRIMARY KEY (id),
  CONSTRAINT analytics_monthly_visits_unique UNIQUE (user_id, host, visit_date),
  CONSTRAINT fk_monthly_visits_app_users
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_monthly_visits_user_host ON analytics_monthly_visits (user_id, host);
CREATE INDEX IF NOT EXISTS idx_monthly_visits_user_date ON analytics_monthly_visits (user_id, visit_date DESC);

-- analytics_monthly_top_urls: top URLs per host per month per status code
CREATE TABLE IF NOT EXISTS analytics_monthly_top_urls (
  id serial NOT NULL,
  user_id uuid NOT NULL,
  host character varying(253) NOT NULL,
  month date NOT NULL,
  path character varying(2048) NOT NULL,
  unique_visits integer NOT NULL DEFAULT 0,
  status_code integer NOT NULL,
  rank integer NOT NULL,
  created_at timestamp without time zone NOT NULL DEFAULT now(),
  CONSTRAINT analytics_monthly_top_urls_pkey PRIMARY KEY (id),
  CONSTRAINT analytics_monthly_top_urls_unique UNIQUE (user_id, host, month, path, status_code),
  CONSTRAINT fk_monthly_top_urls_app_users
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_monthly_top_urls_user_host_month ON analytics_monthly_top_urls (user_id, host, month);
CREATE INDEX IF NOT EXISTS idx_monthly_top_urls_status ON analytics_monthly_top_urls (user_id, host, month, status_code, rank);

-- analytics_monthly_top_referers: top referers per host per month
CREATE TABLE IF NOT EXISTS analytics_monthly_top_referers (
  id serial NOT NULL,
  user_id uuid NOT NULL,
  host character varying(253) NOT NULL,
  month date NOT NULL,
  referer character varying(253) NOT NULL,
  unique_visits integer NOT NULL DEFAULT 0,
  rank integer NOT NULL,
  created_at timestamp without time zone NOT NULL DEFAULT now(),
  CONSTRAINT analytics_monthly_top_referers_pkey PRIMARY KEY (id),
  CONSTRAINT analytics_monthly_top_referers_unique UNIQUE (user_id, host, month, referer),
  CONSTRAINT fk_monthly_top_referers_app_users
    FOREIGN KEY(user_id)
  REFERENCES app_users(id)
  ON DELETE CASCADE
  ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_monthly_referers_user_host_month ON analytics_monthly_top_referers (user_id, host, month);
