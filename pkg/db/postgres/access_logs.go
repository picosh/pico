package postgres

import (
	"time"

	"github.com/picosh/pico/pkg/db"
)

func (me *PsqlDB) FindAccessLogs(userID string, fromDate *time.Time) ([]*db.AccessLog, error) {
	var logs []*db.AccessLog
	err := me.Db.Select(&logs, `SELECT * FROM access_logs WHERE user_id=$1 AND created_at >= $2 ORDER BY created_at ASC`, userID, fromDate)
	if err != nil {
		return nil, err
	}
	return logs, nil
}

func (me *PsqlDB) FindAccessLogsByPubkey(pubkey string, fromDate *time.Time) ([]*db.AccessLog, error) {
	var logs []*db.AccessLog
	err := me.Db.Select(&logs, `SELECT * FROM access_logs WHERE pubkey=$1 AND created_at >= $2 ORDER BY created_at ASC`, pubkey, fromDate)
	if err != nil {
		return nil, err
	}
	return logs, nil
}

func (me *PsqlDB) FindPubkeysInAccessLogs(userID string) ([]string, error) {
	var pubkeys []string
	err := me.Db.Select(&pubkeys, `SELECT DISTINCT(pubkey) FROM access_logs WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	return pubkeys, nil
}

func (me *PsqlDB) InsertAccessLog(log *db.AccessLog) error {
	_, err := me.Db.Exec(
		`INSERT INTO access_logs (user_id, service, pubkey, identity) VALUES ($1, $2, $3, $4);`,
		log.UserID,
		log.Service,
		log.Pubkey,
		log.Identity,
	)
	return err
}
