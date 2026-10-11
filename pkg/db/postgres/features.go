package postgres

import (
	"database/sql"
	"fmt"
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

func (me *PsqlDB) HasFeatureByUser(userID string, feature string) bool {
	ff, err := me.FindFeature(userID, feature)
	if err != nil {
		return false
	}
	return ff.IsValid()
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
	// if the feature flag has already expired we don't want to add a year to it since that will
	// not grant the user a full year
	if ff == nil || !ff.IsValid() {
		t := time.Now()
		return t.AddDate(1, 0, 0)
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

	plus := me.createFeatureExpiresAt(user.ID, "plus")
	plusQuery := fmt.Sprintf(`INSERT INTO feature_flags (user_id, name, data, expires_at, payment_history_id)
	VALUES ($1, 'plus', '{"storage_max":5000000000, "file_max":100000000, "email": "%s"}'::jsonb, $2, $3);`, email)
	_, err = tx.Exec(plusQuery, user.ID, plus, paymentHistoryId)
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
