package pipe

import (
	"fmt"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/postgres"
)

// DB is the storage pipe uses.
type DB interface {
	FindUserByName(name string) (*db.User, error)
	FindUserByToken(token string) (*db.User, error)
	HasFeatureByUser(userID string, feature string) bool
	UpsertPipeMonitor(userID, topic string, dur time.Duration, winEnd *time.Time) error
	UpdatePipeMonitorLastPing(userID, topic string, lastPing *time.Time) error
	RemovePipeMonitor(userID, topic string) error
	FindPipeMonitorByTopic(userID, topic string) (*db.PipeMonitor, error)
	FindPipeMonitorsByUser(userID string) ([]*db.PipeMonitor, error)
	InsertPipeMonitorHistory(monitorID string, windowDur time.Duration, windowEnd, lastPing *time.Time) error
	FindPipeMonitorHistory(monitorID string, from, to time.Time) ([]*db.PipeMonitorHistory, error)
}

// PsqlDB is pipe's postgres storage: the queries apps share, and the ones
// only pipe makes.
type PsqlDB struct {
	*postgres.PsqlDB
}

var _ DB = (*PsqlDB)(nil)

func NewDB(base *postgres.PsqlDB) *PsqlDB {
	return &PsqlDB{PsqlDB: base}
}

func (me *PsqlDB) UpsertPipeMonitor(userID, topic string, dur time.Duration, winEnd *time.Time) error {
	durStr := fmt.Sprintf("%d seconds", int64(dur.Seconds()))
	_, err := me.Db.Exec(
		`INSERT INTO pipe_monitors (user_id, topic, window_dur, window_end)
		VALUES ($1, $2, $3::interval, $4)
		ON CONFLICT (user_id, topic) DO UPDATE SET window_dur = $3::interval, window_end = $4, updated_at = NOW();`,
		userID,
		topic,
		durStr,
		winEnd,
	)
	return err
}

func (me *PsqlDB) UpdatePipeMonitorLastPing(userID, topic string, lastPing *time.Time) error {
	_, err := me.Db.Exec(
		`UPDATE pipe_monitors SET last_ping = $3, updated_at = NOW() WHERE user_id = $1 AND topic = $2;`,
		userID,
		topic,
		lastPing,
	)
	return err
}

func (me *PsqlDB) RemovePipeMonitor(userID, topic string) error {
	_, err := me.Db.Exec(
		`DELETE FROM pipe_monitors WHERE user_id = $1 AND topic = $2;`,
		userID,
		topic,
	)
	return err
}

func (me *PsqlDB) FindPipeMonitorByTopic(userID, topic string) (*db.PipeMonitor, error) {
	monitor := &db.PipeMonitor{}
	err := me.Db.Get(monitor, `SELECT id, user_id, topic, (EXTRACT(EPOCH FROM window_dur) * 1000000000)::bigint as window_dur, window_end, last_ping, created_at, updated_at FROM pipe_monitors WHERE user_id = $1 AND topic = $2;`, userID, topic)
	if err != nil {
		return nil, err
	}
	return monitor, nil
}

func (me *PsqlDB) FindPipeMonitorsByUser(userID string) ([]*db.PipeMonitor, error) {
	var monitors []*db.PipeMonitor
	err := me.Db.Select(&monitors, `SELECT id, user_id, topic, (EXTRACT(EPOCH FROM window_dur) * 1000000000)::bigint as window_dur, window_end, last_ping, created_at, updated_at FROM pipe_monitors WHERE user_id = $1 ORDER BY topic;`, userID)
	if err != nil {
		return nil, err
	}
	return monitors, nil
}

func (me *PsqlDB) InsertPipeMonitorHistory(monitorID string, windowDur time.Duration, windowEnd, lastPing *time.Time) error {
	durStr := fmt.Sprintf("%d seconds", int64(windowDur.Seconds()))
	_, err := me.Db.Exec(
		`INSERT INTO pipe_monitors_history (monitor_id, window_dur, window_end, last_ping) VALUES ($1, $2::interval, $3, $4)`,
		monitorID, durStr, windowEnd, lastPing,
	)
	return err
}

func (me *PsqlDB) FindPipeMonitorHistory(monitorID string, from, to time.Time) ([]*db.PipeMonitorHistory, error) {
	var history []*db.PipeMonitorHistory
	err := me.Db.Select(
		&history,
		`SELECT id, monitor_id, (EXTRACT(EPOCH FROM window_dur) * 1000000000)::bigint as window_dur, window_end, last_ping, created_at, updated_at FROM pipe_monitors_history WHERE monitor_id = $1 AND last_ping <= $2 AND window_end >= $3 ORDER BY last_ping ASC`,
		monitorID, to, from,
	)
	if err != nil {
		return nil, err
	}
	return history, nil
}
