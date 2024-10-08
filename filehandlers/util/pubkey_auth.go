package util

import (
	"log/slog"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

type SshAuthHandler struct {
	DBPool db.DB
	Logger *slog.Logger
	Cfg    *shared.ConfigSite
}

func NewSshAuthHandler(dbpool db.DB, logger *slog.Logger, cfg *shared.ConfigSite) *SshAuthHandler {
	return &SshAuthHandler{
		DBPool: dbpool,
		Logger: logger,
		Cfg:    cfg,
	}
}

func (r *SshAuthHandler) PubkeyAuthHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	shared.SetPublicKeyCtx(ctx, key)

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
