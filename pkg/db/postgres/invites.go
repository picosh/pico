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
	WHERE from_user_id=$1`
	err := me.Db.Select(&invites, query, userID)
	if err != nil {
		return nil, err
	}
	return invites, nil
}

// inviteFeatures are the services an invite grants access to.
var inviteFeatures = []string{"prose", "pgs", "pastes"}

func (me *PsqlDB) hasActiveInvite(userID string) (bool, error) {
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
		invited, err := me.hasActiveInvite(fromUserID)
		if err != nil {
			return err
		}
		canInvite = invited
	}

	if !canInvite {
		return fmt.Errorf("must have valid pico+ membership or an active invite from someone")
	}

	_, err := me.Db.Exec(
		`INSERT INTO invites (from_user_id, to_user_id) VALUES ($1, $2)`,
		fromUserID, toUserID,
	)
	if err != nil {
		return err
	}

	expiresAt := time.Now().Add(5 * 365 * 24 * time.Hour)
	for _, name := range inviteFeatures {
		if _, err := me.InsertFeature(toUserID, name, expiresAt); err != nil {
			return err
		}
	}
	return nil
}
