package postgres

import (
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/picosh/pico/pkg/db"
)

func (me *PsqlDB) insertPublicKeyWithTx(userID, key, name string, tx *sqlx.Tx) error {
	pk, _ := me.findPublicKeyByKey(key)
	if pk != nil {
		return db.ErrPublicKeyTaken
	}
	query := `INSERT INTO public_keys (user_id, public_key, name) VALUES ($1, $2, $3)`
	_, err := tx.Exec(query, userID, key, name)
	return err
}

func (me *PsqlDB) InsertPublicKey(userID, key, name string) error {
	pk, _ := me.findPublicKeyByKey(key)
	if pk != nil {
		return db.ErrPublicKeyTaken
	}
	query := `INSERT INTO public_keys (user_id, public_key, name) VALUES ($1, $2, $3)`
	_, err := me.Db.Exec(query, userID, key, name)
	return err
}

func (me *PsqlDB) UpdatePublicKey(pubkeyID, name string) (*db.PublicKey, error) {
	pk, err := me.findPublicKey(pubkeyID)
	if err != nil {
		return nil, err
	}

	query := `UPDATE public_keys SET name=$1 WHERE id=$2;`
	_, err = me.Db.Exec(query, name, pk.ID)
	if err != nil {
		return nil, err
	}

	pk, err = me.findPublicKey(pubkeyID)
	if err != nil {
		return nil, err
	}
	return pk, nil
}

func (me *PsqlDB) findPublicKeyByKey(key string) (*db.PublicKey, error) {
	var keys []*db.PublicKey
	rs, err := me.Db.Queryx(`SELECT id, user_id, name, public_key, created_at FROM public_keys WHERE public_key = $1`, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()

	for rs.Next() {
		pk := &db.PublicKey{}
		err := rs.Scan(&pk.ID, &pk.UserID, &pk.Name, &pk.Key, &pk.CreatedAt)
		if err != nil {
			return nil, err
		}

		keys = append(keys, pk)
	}

	if rs.Err() != nil {
		return nil, rs.Err()
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("pubkey not found in our database: [%s]", key)
	}

	// When we run PublicKeyByKey and there are multiple public keys returned from the database
	// that should mean that we don't have the correct username for this public key.
	// When that happens we need to reject the authentication and ask the user to provide the correct
	// username when using ssh.  So instead of `ssh <domain>` it should be `ssh user@<domain>`
	if len(keys) > 1 {
		return nil, &db.ErrMultiplePublicKeys{}
	}

	return keys[0], nil
}

func (me *PsqlDB) findPublicKey(pubkeyID string) (*db.PublicKey, error) {
	pk := &db.PublicKey{}
	err := me.Db.Get(pk, `SELECT * FROM public_keys WHERE id = $1`, pubkeyID)
	if err != nil {
		return nil, err
	}
	return pk, nil
}

func (me *PsqlDB) FindKeysByUser(user *db.User) ([]*db.PublicKey, error) {
	var keys []*db.PublicKey
	err := me.Db.Select(&keys, `SELECT * FROM public_keys WHERE user_id = $1 ORDER BY created_at ASC`, user.ID)
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func (me *PsqlDB) RemoveKeys(keyIDs []string) error {
	param := "{" + strings.Join(keyIDs, ",") + "}"
	_, err := me.Db.Exec(`DELETE FROM public_keys WHERE id = ANY($1::uuid[])`, param)
	return err
}
