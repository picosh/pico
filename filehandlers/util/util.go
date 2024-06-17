package util

import (
	"fmt"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
)

type ctxUserKey struct{}
type ctxFeatureFlagKey struct{}

func GetUser(ctx ssh.Context) (*db.User, error) {
	user, ok := ctx.Value(ctxUserKey{}).(*db.User)
	if !ok {
		return user, fmt.Errorf("user not set on `ssh.Context()` for connection")
	}
	return user, nil
}

func SetUser(ctx ssh.Context, user *db.User) {
	ctx.SetValue(ctxUserKey{}, user)
}

func GetFeatureFlag(ctx ssh.Context) (*db.FeatureFlag, error) {
	ff, ok := ctx.Value(ctxFeatureFlagKey{}).(*db.FeatureFlag)
	if !ok || ff.Name == "" {
		return ff, fmt.Errorf("feature flag not set on `ssh.Context()` for connection")
	}
	return ff, nil
}

func SetFeatureFlag(ctx ssh.Context, ff *db.FeatureFlag) {
	ctx.SetValue(ctxFeatureFlagKey{}, ff)
}
