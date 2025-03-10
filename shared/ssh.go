package shared

import (
	"fmt"
	"log/slog"

	"github.com/picosh/pico/db"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
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

func (r *SshAuthHandler) PubkeyAuthHandler(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	pubkey := utils.KeyForKeyText(key)
	user, err := r.DB.FindUserByPubkey(pubkey)
	if err != nil {
		r.Logger.Error(
			"could not find user for key",
			"keyType", key.Type(),
			"key", string(key.Marshal()),
			"err", err,
		)
		return nil, err
	}

	if user.Name == "" {
		r.Logger.Error("username is not set")
		return nil, fmt.Errorf("username is not set")
	}

	return &ssh.Permissions{
		Extensions: map[string]string{
			"user_id": user.ID,
			"pubkey":  pubkey,
		},
	}, nil
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
