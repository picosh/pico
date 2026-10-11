package postgres

import (
	"github.com/picosh/pico/pkg/db"
)

func (me *PsqlDB) InsertTunsEventLog(log *db.TunsEventLog) error {
	_, err := me.Db.Exec(
		`INSERT INTO tuns_event_logs
			(user_id, server_id, remote_addr, event_type, tunnel_type, connection_type, tunnel_id)
		VALUES
			($1, $2, $3, $4, $5, $6, $7)`,
		log.UserId, log.ServerID, log.RemoteAddr, log.EventType, log.TunnelType,
		log.ConnectionType, log.TunnelID,
	)
	return err
}

func (me *PsqlDB) FindTunsEventLogsByAddr(userID, addr string) ([]*db.TunsEventLog, error) {
	var logs []*db.TunsEventLog
	err := me.Db.Select(&logs,
		`SELECT * FROM tuns_event_logs WHERE user_id=$1 AND tunnel_id=$2 ORDER BY created_at DESC`, userID, addr)
	if err != nil {
		return nil, err
	}
	return logs, nil
}

func (me *PsqlDB) FindTunsEventLogs(userID string) ([]*db.TunsEventLog, error) {
	var logs []*db.TunsEventLog
	err := me.Db.Select(&logs,
		`SELECT * FROM tuns_event_logs WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return logs, nil
}
