package shared

import (
	"log/slog"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/utils"
)

type SshAuthHandler struct {
	DB     AuthFindUser
	Logger *slog.Logger
}

type AuthFindUser interface {
	FindUserByPubkey(key string) (*db.User, error)
}

func NewSshAuthHandler(dbh AuthFindUser, logger *slog.Logger) *SshAuthHandler {
	return &SshAuthHandler{
		DB:     dbh,
		Logger: logger,
	}
}

func (r *SshAuthHandler) PubkeyAuthHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	pubkey := utils.KeyForKeyText(key)
	user, err := r.DB.FindUserByPubkey(pubkey)
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

	if ctx.Permissions().Extensions == nil {
		ctx.Permissions().Extensions = map[string]string{}
	}
	ctx.Permissions().Extensions["user_id"] = user.ID
	ctx.Permissions().Extensions["pubkey"] = pubkey
	return true
}

func FindPlusFF(dbpool db.DB, cfg *ConfigSite, userID string) *db.FeatureFlag {
	ff, _ := dbpool.FindFeatureForUser(userID, "plus")
	// we have free tiers so users might not have a feature flag
	// in which case we set sane defaults
	if ff == nil {
		ff = db.NewFeatureFlag(
			userID,
			"plus",
			cfg.MaxSize,
			cfg.MaxAssetSize,
			cfg.MaxSpecialFileSize,
		)
	}
	// this is jank
	ff.Data.StorageMax = ff.FindStorageMax(cfg.MaxSize)
	ff.Data.FileMax = ff.FindFileMax(cfg.MaxAssetSize)
	ff.Data.SpecialFileMax = ff.FindSpecialFileMax(cfg.MaxSpecialFileSize)
	return ff
}
