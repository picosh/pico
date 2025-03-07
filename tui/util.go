package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/utils"
)

func findUser(shrd *common.SharedModel) (*db.User, error) {
	logger := shrd.Cfg.Logger
	var user *db.User
	usr := shrd.Session.User()

	if shrd.Session.PublicKey() == nil {
		return nil, fmt.Errorf("unable to find public key")
	}

	key := utils.KeyForKeyText(shrd.Session.PublicKey())

	user, err := shrd.Dbpool.FindUserForKey(usr, key)
	if err != nil {
		logger.Error("no user found for public key", "err", err.Error())
		// we only want to throw an error for specific cases
		if errors.Is(err, &db.ErrMultiplePublicKeys{}) {
			return nil, err
		}
		// no user and not error indicates we need to create an account
		return nil, nil
	}

	// impersonation
	adminPrefix := "admin__"
	if strings.HasPrefix(usr, adminPrefix) {
		hasFeature := shrd.Dbpool.HasFeatureForUser(user.ID, "admin")
		if !hasFeature {
			return nil, fmt.Errorf("only admins can impersonate a user")
		}
		impersonate := strings.Replace(usr, adminPrefix, "", 1)
		user, err = shrd.Dbpool.FindUserForName(impersonate)
		if err != nil {
			return nil, err
		}
		shrd.Impersonated = true
	}

	return user, nil
}

func findPlusFeatureFlag(shrd *common.SharedModel) (*db.FeatureFlag, error) {
	return findFeatureFlag(shrd, "plus")
}

func findFeatureFlag(shrd *common.SharedModel, feature string) (*db.FeatureFlag, error) {
	if shrd.User == nil {
		return nil, nil
	}

	ff, err := shrd.Dbpool.FindFeatureForUser(shrd.User.ID, feature)
	if err != nil {
		return nil, err
	}

	return ff, nil
}
