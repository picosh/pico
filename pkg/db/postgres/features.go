package postgres

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/picosh/pico/pkg/db"
)

func (me *PsqlDB) FindFeature(userID string, feature string) (*db.FeatureFlag, error) {
	ff := &db.FeatureFlag{}
	err := me.Db.Get(ff, `SELECT * FROM feature_flags WHERE user_id = $1 AND name = $2 ORDER BY expires_at DESC LIMIT 1`, userID, feature)
	if err != nil {
		return nil, err
	}
	return ff, nil
}

func (me *PsqlDB) FindFeaturesByUser(userID string) ([]*db.FeatureFlag, error) {
	var features []*db.FeatureFlag
	// https://stackoverflow.com/a/16920077
	query := `SELECT DISTINCT ON (name) *
		FROM feature_flags
		WHERE user_id=$1
		ORDER BY name, expires_at DESC;`
	err := me.Db.Select(&features, query, userID)
	if err != nil {
		return nil, err
	}
	return features, nil
}

func (me *PsqlDB) InsertFeature(userID, name string, expiresAt time.Time) (*db.FeatureFlag, error) {
	var featureID string
	err := me.Db.QueryRow(
		`INSERT INTO feature_flags (user_id, name, expires_at) VALUES ($1, $2, $3) RETURNING id;`,
		userID,
		name,
		expiresAt,
	).Scan(&featureID)
	if err != nil {
		return nil, err
	}

	feature, err := me.FindFeature(userID, name)
	if err != nil {
		return nil, err
	}

	return feature, nil
}

func (me *PsqlDB) RemoveFeature(userID string, name string) error {
	_, err := me.Db.Exec(`DELETE FROM feature_flags WHERE user_id = $1 AND name = $2`, userID, name)
	return err
}

func (me *PsqlDB) createFeatureExpiresAt(userID, name string) time.Time {
	ff, _ := me.FindFeature(userID, name)
	return featureExpiresAt(ff)
}

// featureExpiresAt is a year past a valid flag's expiry, or a year from now.
// An expired flag doesn't count, since adding to it wouldn't grant a full
// year.
func featureExpiresAt(ff *db.FeatureFlag) time.Time {
	if ff == nil || !ff.IsValid() {
		return time.Now().AddDate(1, 0, 0)
	}
	return ff.ExpiresAt.AddDate(1, 0, 0)
}

func (me *PsqlDB) AddPicoPlusUser(username, email, paymentType, txId string) error {
	user, err := me.FindUserByName(username)
	if err != nil {
		return err
	}

	tx, err := me.Db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// Serializes upgrades for a user, so a redelivered webhook sees the
	// payment the first delivery recorded.
	if _, err := tx.Exec(`SELECT id FROM app_users WHERE id = $1 FOR UPDATE`, user.ID); err != nil {
		return err
	}

	if paymentType != "" && txId != "" {
		var recorded bool
		err := tx.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM payment_history WHERE user_id = $1 AND payment_type = $2 AND data->>'tx_id' = $3)`,
			user.ID, paymentType, txId,
		).Scan(&recorded)
		if err != nil {
			return err
		}
		if recorded {
			return db.ErrPaymentRecorded
		}
	}

	var paymentHistoryId sql.NullString
	if paymentType != "" {
		data := db.PaymentHistoryData{
			Notes: "",
			TxID:  txId,
		}

		err := tx.QueryRow(
			`INSERT INTO payment_history (user_id, payment_type, amount, data) VALUES ($1, $2, 24 * 1000000, $3) RETURNING id;`,
			user.ID,
			paymentType,
			data,
		).Scan(&paymentHistoryId)
		if err != nil {
			return err
		}
	}

	// Read in the transaction, which holds the user's lock.
	current := &db.FeatureFlag{}
	if err := tx.Get(current, `SELECT * FROM feature_flags WHERE user_id = $1 AND name = 'plus' ORDER BY expires_at DESC LIMIT 1`, user.ID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		current = nil
	}
	plus := featureExpiresAt(current)
	data, err := json.Marshal(map[string]any{
		"storage_max": 5000000000,
		"file_max":    100000000,
		"email":       email,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`INSERT INTO feature_flags (user_id, name, data, expires_at, payment_history_id)
		VALUES ($1, 'plus', $2::jsonb, $3, $4);`,
		user.ID, string(data), plus, paymentHistoryId,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (me *PsqlDB) AddFeatureUser(username, name string) error {
	user, err := me.FindUserByName(username)
	if err != nil {
		return err
	}

	expiresAt := me.createFeatureExpiresAt(user.ID, name)
	_, err = me.InsertFeature(user.ID, name, expiresAt)
	return err
}
