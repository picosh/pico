package util

import (
	"fmt"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
)

type ctxUserKey struct{}
type ctxFeatureFlagKey struct{}

func GetUser(s ssh.Session) (*db.User, error) {
	user, ok := s.Context().Value(ctxUserKey{}).(*db.User)
	if !ok {
		return user, fmt.Errorf("user not set on `ssh.Context()` for connection")
	}
	return user, nil
}

func SetUser(s ssh.Session, user *db.User) {
	s.Context().SetValue(ctxUserKey{}, user)
}

func GetFeatureFlag(s ssh.Session) (*db.FeatureFlag, error) {
	ff, ok := s.Context().Value(ctxFeatureFlagKey{}).(*db.FeatureFlag)
	if !ok || ff.Name == "" {
		return ff, fmt.Errorf("feature flag not set on `ssh.Context()` for connection")
	}
	return ff, nil
}

func SetFeatureFlag(s ssh.Session, ff *db.FeatureFlag) {
	s.Context().SetValue(ctxFeatureFlagKey{}, ff)
}
