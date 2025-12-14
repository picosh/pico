package shared

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
)

const adminPrefix = "admin__"

type SshAuthHandler struct {
	DB        AuthFindUser
	Logger    *slog.Logger
	Principal string
}

type AuthFindUser interface {
	FindUserByPubkey(key string) (*db.User, error)
	FindUserByName(name string) (*db.User, error)
	FindFeature(userID, name string) (*db.FeatureFlag, error)
}

func NewSshAuthHandler(dbh AuthFindUser, logger *slog.Logger, principal string) *SshAuthHandler {
	return &SshAuthHandler{
		DB:        dbh,
		Logger:    logger,
		Principal: principal,
	}
}

func PubkeyCertVerify(key ssh.PublicKey, srcPrincipal string) (string, error) {
	cert, ok := key.(*ssh.Certificate)
	if ok {
		if cert.CertType != ssh.UserCert {
			return "", fmt.Errorf("ssh-cert has type %d", cert.CertType)
		}

		found := false
		for _, princ := range cert.ValidPrincipals {
			if princ == "admin" || princ == srcPrincipal {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("ssh-cert principals not valid")
		}

		clock := time.Now
		unixNow := clock().Unix()
		if after := int64(cert.ValidAfter); after < 0 || unixNow < int64(cert.ValidAfter) {
			return "", fmt.Errorf("ssh-cert is not yet valid")
		}
		if before := int64(cert.ValidBefore); cert.ValidBefore != uint64(ssh.CertTimeInfinity) && (unixNow >= before || before < 0) {
			return "", fmt.Errorf("ssh-cert has expired")
		}

		return utils.KeyForKeyText(cert.SignatureKey), nil
	}

	return utils.KeyForKeyText(key), nil
}

func (r *SshAuthHandler) PubkeyAuthHandler(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	log := r.Logger
	var user *db.User
	var err error
	pubkey, err := PubkeyCertVerify(key, r.Principal)
	if err != nil {
		return nil, err
	}

	user, err = r.DB.FindUserByPubkey(pubkey)
	if err != nil {
		log.Error(
			"could not find user for key",
			"keyType", key.Type(),
			"key", string(key.Marshal()),
			"err", err,
		)
		return nil, err
	}

	if user.Name == "" {
		log.Error("username is not set")
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
