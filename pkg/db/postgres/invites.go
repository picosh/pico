package postgres

import (
	"fmt"
	"time"

	"github.com/picosh/pico/pkg/db"
)

func (me *PsqlDB) FindWhoInvitedUser(toUserID string) (string, error) {
	var inviterName string
	query := `
        SELECT app_users.name
        FROM invites
        LEFT JOIN app_users ON app_users.id = invites.from_user_id
        WHERE to_user_id=$1
    `
	err := me.Db.Get(&inviterName, query, toUserID)
	if err != nil {
		return "", err
	}

	return inviterName, nil
}

func (me *PsqlDB) FindInvitesByUser(userID string) ([]*db.Invite, error) {
	var invites []*db.Invite
	query := `SELECT
		invites.id, invites.from_user_id, invites.to_user_id, invites.created_at,
		app_users.name as to_user_name
	FROM invites
	LEFT JOIN app_users ON app_users.id = invites.to_user_id
	WHERE from_user_id=$1
	ORDER BY invites.created_at DESC, invites.id DESC`
	err := me.Db.Select(&invites, query, userID)
	if err != nil {
		return nil, err
	}
	return invites, nil
}

// inviteFeatures are the services an invite grants access to.
var inviteFeatures = []string{"prose", "pgs", "pastes"}

func (me *PsqlDB) HasActiveInvite(userID string) (bool, error) {
	var invited bool
	err := me.Db.Get(&invited, `SELECT EXISTS (SELECT 1 FROM invites WHERE to_user_id = $1)`, userID)
	if err != nil || !invited {
		return false, err
	}
	for _, name := range inviteFeatures {
		if db.HasFeature(me, userID, name) {
			return true, nil
		}
	}
	return false, nil
}

func (me *PsqlDB) InviteUser(fromUserID string, toUserID string) error {
	canInvite := db.HasFeature(me, fromUserID, "plus")
	if !canInvite {
		invited, err := me.HasActiveInvite(fromUserID)
		if err != nil {
			return err
		}
		canInvite = invited
	}

	if !canInvite {
		return fmt.Errorf("must have valid pico+ membership or an active invite from someone")
	}

	tx, err := me.Db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.Exec(
		`INSERT INTO invites (from_user_id, to_user_id) VALUES ($1, $2)
		ON CONFLICT (to_user_id) DO NOTHING`,
		fromUserID, toUserID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("user has already been invited")
	}

	expiresAt := time.Now().Add(5 * 365 * 24 * time.Hour)
	for _, name := range inviteFeatures {
		if _, err := tx.Exec(
			`INSERT INTO feature_flags (user_id, name, expires_at) VALUES ($1, $2, $3)`,
			toUserID, name, expiresAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
