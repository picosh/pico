package postgres

import (
	"github.com/picosh/pico/pkg/db"
)

func (me *PsqlDB) FindUserByToken(token string) (*db.User, error) {
	user := &db.User{}
	err := me.Db.Get(user, `
	SELECT app_users.id, app_users.name, app_users.created_at
	FROM app_users
	LEFT JOIN tokens ON tokens.user_id = app_users.id
	WHERE tokens.token = $1 AND tokens.expires_at > NOW()`, token)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (me *PsqlDB) InsertToken(userID, name string) (string, error) {
	var token string
	err := me.Db.QueryRow(`INSERT INTO tokens (user_id, name) VALUES($1, $2) RETURNING token;`, userID, name).Scan(&token)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (me *PsqlDB) UpsertToken(userID, name string) (string, error) {
	token, _ := me.findTokenByName(userID, name)
	if token != "" {
		return token, nil
	}

	token, err := me.InsertToken(userID, name)
	return token, err
}

func (me *PsqlDB) findTokenByName(userID, name string) (string, error) {
	var token string
	err := me.Db.QueryRow(`SELECT token FROM tokens WHERE user_id = $1 AND name = $2`, userID, name).Scan(&token)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (me *PsqlDB) RemoveToken(tokenID string) error {
	_, err := me.Db.Exec(`DELETE FROM tokens WHERE id = $1`, tokenID)
	return err
}

func (me *PsqlDB) FindTokensByUser(userID string) ([]*db.Token, error) {
	var tokens []*db.Token
	err := me.Db.Select(&tokens, `SELECT * FROM tokens WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	return tokens, nil
}
