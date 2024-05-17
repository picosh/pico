package tui

import (
	"errors"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui/common"
)

func findUser(shrd common.SharedModel) (*db.User, error) {
	logger := shrd.Cfg.Logger
	var user *db.User
	usr := shrd.Session.User()

	key, err := shared.KeyForKeyText(shrd.Session.PublicKey())
	if err != nil {
		return nil, err
	}

	user, err = shrd.Dbpool.FindUserForKey(usr, key)
	if err != nil {
		logger.Error("no user found for public key", "err", err.Error())
		// we only want to throw an error for specific cases
		if errors.Is(err, &db.ErrMultiplePublicKeys{}) {
			return nil, err
		}
		// no user and not error indicates we need to create an account
		return nil, nil
	}

	return user, nil
}

func findPlusFeatureFlag(shrd common.SharedModel) (*db.FeatureFlag, error) {
	if shrd.User == nil {
		return nil, nil
	}

	ff, err := shrd.Dbpool.FindFeatureForUser(shrd.User.ID, "plus")
	if err != nil {
		return nil, err
	}

	return ff, nil
}
