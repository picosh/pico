package pgsdb

import (
	"fmt"
	"log/slog"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

var sqliteSchema = `
CREATE TABLE IF NOT EXISTS app_users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(name)
);

CREATE TABLE IF NOT EXISTS public_keys (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	public_key TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE (user_id, public_key),
	CONSTRAINT public_keys_user_id_fk
		FOREIGN KEY(user_id) REFERENCES app_users(id)
		ON DELETE CASCADE
		ON UPDATE CASCADE
);

CREATE TABLE IF NOT EXISTS projects (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	project_dir TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	acl BLOB DEFAULT '{"data": [], "type": "public"}' NOT NULL,
	blocked TEXT NOT NULL DEFAULT '',
	UNIQUE (user_id, name),
	CONSTRAINT projects_user_id_fk
		FOREIGN KEY(user_id) REFERENCES app_users(id)
		ON DELETE CASCADE
		ON UPDATE CASCADE
);

CREATE TABLE IF NOT EXISTS feature_flags (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	expires_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	data BLOB DEFAULT '{}' NOT NULL,
	payment_history_id INTEGER,
	CONSTRAINT feature_flags_user_id_fk
		FOREIGN KEY(user_id) REFERENCES app_users(id)
		ON DELETE CASCADE
		ON UPDATE CASCADE
);
`

var sqliteMigrations = []string{
	"", // migration #0 is reserved for schema initialization
}

func NewSqliteDB(databaseUrl string, logger *slog.Logger) (*PgsPsqlDB, error) {
	var err error
	d := &PgsPsqlDB{
		Logger: logger,
	}
	d.Logger.Info("connecting to sqlite", "databaseUrl", databaseUrl)

	db, err := SqliteOpen(databaseUrl, logger)
	if err != nil {
		return nil, err
	}

	d.Db = db
	return d, nil
}

// Open opens a database connection.
func SqliteOpen(dsn string, logger *slog.Logger) (*sqlx.DB, error) {
	logger.Info("opening db file", "dsn", dsn)
	db, err := sqlx.Connect("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	err = sqliteUpgrade(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func sqliteUpgrade(db *sqlx.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("failed to query schema version: %v", err)
	}

	if version == len(sqliteMigrations) {
		return nil
	} else if version > len(sqliteMigrations) {
		return fmt.Errorf("(version %d) older than schema (version %d)", len(sqliteMigrations), version)
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if version == 0 {
		if _, err := tx.Exec(sqliteSchema); err != nil {
			return fmt.Errorf("failed to initialize schema: %v", err)
		}
	} else {
		for i := version; i < len(sqliteMigrations); i++ {
			if _, err := tx.Exec(sqliteMigrations[i]); err != nil {
				return fmt.Errorf("failed to execute migration #%v: %v", i, err)
			}
		}
	}

	// For some reason prepared statements don't work here
	_, err = tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", len(sqliteMigrations)))
	if err != nil {
		return fmt.Errorf("failed to bump schema version: %v", err)
	}

	return tx.Commit()
}
