package shared

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
)

const adminPrefix = "admin__"

type SshAuthHandler struct {
	DB     AuthFindUser
	Logger *slog.Logger
}

type AuthFindUser interface {
	FindUserByPubkey(key string) (*db.User, error)
	FindUserByName(name string) (*db.User, error)
	FindFeature(userID, name string) (*db.FeatureFlag, error)
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

	// impersonation
	var impID string
	usr := conn.User()
	if strings.HasPrefix(usr, adminPrefix) {
		ff, err := r.DB.FindFeature(user.ID, "admin")
		if err == nil && ff.IsValid() {
			impersonate := strings.TrimPrefix(usr, adminPrefix)
			impersonatedUser, err := r.DB.FindUserByName(impersonate)
			if err == nil {
				impID = user.ID
				user = impersonatedUser
			}
		}
	}

	perms := &ssh.Permissions{
		Extensions: map[string]string{
			"user_id": user.ID,
			"pubkey":  pubkey,
		},
	}

	if impID != "" {
		perms.Extensions["imp_id"] = impID
	}

	return perms, nil
}

func FindPlusFF(dbpool db.DB, cfg *ConfigSite, userID string) *db.FeatureFlag {
	ff, _ := dbpool.FindFeature(userID, "plus")
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
