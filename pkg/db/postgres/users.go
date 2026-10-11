package postgres

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/picosh/pico/pkg/db"
)

func (me *PsqlDB) shouldBlockSingup(ip string) error {
	blocked := &db.BlockSignups{}
	err := me.Db.Get(blocked, `SELECT * FROM block_signups WHERE ip = $1`, ip)
	// an error in this case means the result is empty (no record found)
	if err != nil {
		return nil
	}
	return fmt.Errorf("your IP address has been blocked: %s", blocked.Reason)
}

func (me *PsqlDB) RegisterUser(username, pubkey, comment, ip string) (*db.User, error) {
	lowerName := strings.ToLower(username)
	valid, err := me.validateName(lowerName)
	if !valid {
		return nil, err
	}

	me.Logger.Info("checking if ip is in block list", "ip", ip, "username", username)
	err = me.shouldBlockSingup(ip)
	if err != nil {
		me.Logger.Warn("user has been blocked from signing up", "ip", ip, "username", username, "err", err)
		return nil, err
	}

	tx, err := me.Db.Beginx()
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var id string
	err = tx.QueryRow(`INSERT INTO app_users (name) VALUES($1) returning id`, lowerName).Scan(&id)
	if err != nil {
		return nil, err
	}

	err = me.insertPublicKeyWithTx(id, pubkey, comment, tx)
	if err != nil {
		return nil, err
	}

	err = tx.Commit()
	if err != nil {
		return nil, err
	}

	return me.FindUserByKey(username, pubkey)
}

func (me *PsqlDB) FindUserByKey(username string, key string) (*db.User, error) {
	me.Logger.Info("attempting to find user with only public key", "key", key)
	pk, err := me.findPublicKeyByKey(key)
	if err == nil {
		me.Logger.Info("found pubkey, looking for user", "key", key, "userId", pk.UserID)
		user, err := me.FindUser(pk.UserID)
		if err != nil {
			return nil, err
		}
		user.PublicKey = pk
		return user, nil
	}

	if errors.Is(err, &db.ErrMultiplePublicKeys{}) {
		me.Logger.Info("detected multiple users with same public key", "user", username)
		user, err := me.findUserForNameAndKey(username, key)
		if err != nil {
			me.Logger.Info("could not find user by username and public key", "user", username, "key", key)
			// this is a little hacky but if we cannot find a user by name and public key
			// then we return the multiple keys detected error so the user knows to specify their
			// when logging in
			return nil, &db.ErrMultiplePublicKeys{}
		}
		return user, nil
	}

	return nil, err
}

func (me *PsqlDB) FindUserByPubkey(key string) (*db.User, error) {
	me.Logger.Info("attempting to find user with only public key", "key", key)
	pk, err := me.findPublicKeyByKey(key)
	if err != nil {
		return nil, err
	}

	me.Logger.Info("found pubkey, looking for user", "key", key, "userId", pk.UserID)
	user, err := me.FindUser(pk.UserID)
	if err != nil {
		return nil, err
	}
	user.PublicKey = pk
	return user, nil
}

func (me *PsqlDB) FindUser(userID string) (*db.User, error) {
	user := &db.User{}
	err := me.Db.Get(user, `SELECT id, COALESCE(name, '') as name, created_at FROM app_users WHERE id = $1`, userID)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (me *PsqlDB) validateName(name string) (bool, error) {
	lower := strings.ToLower(name)
	if slices.Contains(db.DenyList, lower) {
		return false, fmt.Errorf("%s is on deny list: %w", lower, db.ErrNameDenied)
	}
	v := db.NameValidator.MatchString(lower)
	if !v {
		return false, fmt.Errorf("%s is invalid: %w", lower, db.ErrNameInvalid)
	}
	user, _ := me.FindUserByName(lower)
	if user == nil {
		return true, nil
	}
	return false, fmt.Errorf("%s already taken: %w", lower, db.ErrNameTaken)
}

func (me *PsqlDB) FindUserByName(name string) (*db.User, error) {
	user := &db.User{}
	err := me.Db.Get(user, `SELECT * FROM app_users WHERE name = $1`, strings.ToLower(name))
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (me *PsqlDB) findUserForNameAndKey(name string, key string) (*db.User, error) {
	user := &db.User{}
	pk := &db.PublicKey{}

	r := me.Db.QueryRow(`SELECT app_users.id, app_users.name, app_users.created_at, public_keys.id as pk_id, public_keys.public_key, public_keys.created_at as pk_created_at FROM app_users LEFT JOIN public_keys ON public_keys.user_id = app_users.id WHERE app_users.name = $1 AND public_keys.public_key = $2`, strings.ToLower(name), key)
	err := r.Scan(&user.ID, &user.Name, &user.CreatedAt, &pk.ID, &pk.Key, &pk.CreatedAt)
	if err != nil {
		return nil, err
	}

	user.PublicKey = pk
	return user, nil
}

func (me *PsqlDB) FindUsers() ([]*db.User, error) {
	var users []*db.User
	err := me.Db.Select(&users, `SELECT id, COALESCE(name, '') as name, created_at FROM app_users ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	return users, nil
}

func (me *PsqlDB) FindUserStats(userID string) (*db.UserStats, error) {
	stats := db.UserStats{}
	rs, err := me.Db.Queryx(`SELECT cur_space, count(id), min(created_at), max(created_at), max(updated_at) FROM posts WHERE user_id=$1 GROUP BY cur_space`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()

	for rs.Next() {
		stat := db.UserServiceStats{}
		err := rs.Scan(&stat.Service, &stat.Num, &stat.FirstCreatedAt, &stat.LastestCreatedAt, &stat.LatestUpdatedAt)
		if err != nil {
			return nil, err
		}
		switch stat.Service {
		case "prose":
			stats.Prose = stat
		case "pastes":
			stats.Pastes = stat
		case "feeds":
			stats.Feeds = stat
		}
	}

	if rs.Err() != nil {
		return nil, rs.Err()
	}

	pgs, err := me.findPagesStats(userID)
	if err != nil {
		return nil, err
	}
	stats.Pages = *pgs
	return &stats, err
}
