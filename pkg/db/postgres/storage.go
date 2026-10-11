package postgres

import (
	"database/sql"
	"log/slog"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

var PAGER_SIZE = 15

type PsqlDB struct {
	Logger *slog.Logger
	Db     *sqlx.DB
}

type RowScanner interface {
	Scan(dest ...any) error
}

func NewDB(databaseUrl string, logger *slog.Logger) *PsqlDB {
	var err error
	d := &PsqlDB{
		Logger: logger,
	}
	d.Logger.Info("Connecting to postgres", "databaseUrl", databaseUrl)

	db, err := sqlx.Connect("postgres", databaseUrl)
	if err != nil {
		d.Logger.Error(err.Error())
	}
	d.Db = db
	return d
}

func (me *PsqlDB) Close() error {
	me.Logger.Info("Closing db")
	return me.Db.Close()
}

func newNullString(s string) sql.NullString {
	if len(s) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{
		String: s,
		Valid:  true,
	}
}
