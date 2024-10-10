package shared

import (
	"fmt"
	"log/slog"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/utils"
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

type ctxPublicKey struct{}

func GetPublicKey(ctx ssh.Context) (ssh.PublicKey, error) {
	pk, ok := ctx.Value(ctxPublicKey{}).(ssh.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key not set on `ssh.Context()` for connection")
	}
	return pk, nil
}

func SetPublicKey(ctx ssh.Context, pk ssh.PublicKey) {
	ctx.SetValue(ctxPublicKey{}, pk)
}

type SshAuthHandler struct {
	DBPool db.DB
	Logger *slog.Logger
	Cfg    *ConfigSite
}

func NewSshAuthHandler(dbpool db.DB, logger *slog.Logger, cfg *ConfigSite) *SshAuthHandler {
	return &SshAuthHandler{
		DBPool: dbpool,
		Logger: logger,
		Cfg:    cfg,
	}
}

func (r *SshAuthHandler) PubkeyAuthHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	SetPublicKey(ctx, key)

	pubkey := utils.KeyForKeyText(key)

	user, err := r.DBPool.FindUserForKey(ctx.User(), pubkey)
	if err != nil {
		r.Logger.Error(
			"could not find user for key",
			"key", key,
			"err", err,
		)
		return false
	}

	if user.Name == "" {
		r.Logger.Error("username is not set")
		return false
	}

	ff, _ := r.DBPool.FindFeatureForUser(user.ID, "plus")
	// we have free tiers so users might not have a feature flag
	// in which case we set sane defaults
	if ff == nil {
		ff = db.NewFeatureFlag(
			user.ID,
			"plus",
			r.Cfg.MaxSize,
			r.Cfg.MaxAssetSize,
		)
	}
	// this is jank
	ff.Data.StorageMax = ff.FindStorageMax(r.Cfg.MaxSize)
	ff.Data.FileMax = ff.FindFileMax(r.Cfg.MaxAssetSize)

	SetUser(ctx, user)
	SetFeatureFlag(ctx, ff)
	return true
}
