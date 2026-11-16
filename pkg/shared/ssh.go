package shared

import (
	"fmt"
	"log/slog"
	"net"
	"strings"

	"github.com/picosh/pico/pkg/db"
	"golang.org/x/crypto/ssh"
)

const adminPrefix = "admin__"

type SshAuthHandler struct {
	DB        db.AuthDB
	Logger    *slog.Logger
	Principal string
}

func NewSshAuthHandler(dbh db.AuthDB, logger *slog.Logger, principal string) *SshAuthHandler {
	return &SshAuthHandler{
		DB:        dbh,
		Logger:    logger,
		Principal: principal,
	}
}

type AuthedPubkey struct {
	OrigPubkey string
	Pubkey     string
	Identity   string
	// CriticalOptions holds a cert's source-address restriction, which the
	// caller enforces against the client's address.
	CriticalOptions map[string]string
}

const sourceAddressOption = "source-address"

// certChecker verifies a cert's CA signature, validity window and critical
// options.
var certChecker = &ssh.CertChecker{
	SupportedCriticalOptions: []string{sourceAddressOption},
}

func PubkeyCertVerify(key ssh.PublicKey, srcPrincipal string) (*AuthedPubkey, error) {
	origPubkey := KeyForKeyText(key)
	authed := &AuthedPubkey{
		OrigPubkey: origPubkey,
		Pubkey:     origPubkey,
		Identity:   "pubkey",
	}

	cert, ok := key.(*ssh.Certificate)
	if ok {
		if cert.CertType != ssh.UserCert {
			return nil, fmt.Errorf("ssh-cert has type %d", cert.CertType)
		}

		principal := ""
		for _, princ := range cert.ValidPrincipals {
			if princ == "admin" || princ == srcPrincipal {
				principal = princ
				break
			}
		}
		if principal == "" {
			return nil, fmt.Errorf("ssh-cert principals not valid")
		}

		// The account is found by the CA key, so the cert must be signed by it.
		if err := certChecker.CheckCert(principal, cert); err != nil {
			return nil, fmt.Errorf("ssh-cert invalid: %w", err)
		}

		authed.Pubkey = KeyForKeyText(cert.SignatureKey)
		authed.Identity = cert.KeyId
		authed.CriticalOptions = cert.CriticalOptions
		return authed, nil
	}

	return authed, nil
}

// CheckSourceAddress enforces a cert's source-address restriction on a client
// address given as "host:port" or a bare IP.
func (a *AuthedPubkey) CheckSourceAddress(remoteAddr string) error {
	sourceAddrs, ok := a.CriticalOptions[sourceAddressOption]
	if !ok {
		return nil
	}

	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("ssh-cert requires source-address but client address %q is unknown", remoteAddr)
	}

	for _, allowed := range strings.Split(sourceAddrs, ",") {
		if allowedIP := net.ParseIP(allowed); allowedIP != nil {
			if allowedIP.Equal(ip) {
				return nil
			}
			continue
		}
		_, ipNet, err := net.ParseCIDR(allowed)
		if err != nil {
			return fmt.Errorf("ssh-cert has invalid source-address %q: %w", allowed, err)
		}
		if ipNet.Contains(ip) {
			return nil
		}
	}
	return fmt.Errorf("ssh-cert source-address does not allow %s", ip)
}

func (r *SshAuthHandler) PubkeyAuthHandler(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	log := r.Logger
	var user *db.User
	var err error
	authed, err := PubkeyCertVerify(key, r.Principal)
	if err != nil {
		return nil, err
	}

	user, err = r.DB.FindUserByPubkey(authed.Pubkey)
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

	if authed.Identity == "pubkey" && user.PublicKey != nil && user.PublicKey.Name != "" {
		authed.Identity = user.PublicKey.Name
	}

	log.Info("inserting access log", "principal", r.Principal, "identity", authed.Identity)
	err = r.DB.InsertAccessLog(&db.AccessLog{
		UserID:   user.ID,
		Service:  r.Principal,
		Identity: authed.Identity,
		Pubkey:   authed.OrigPubkey,
	})
	if err != nil {
		log.Error("cannot insert access log", "err", err)
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
		// x/crypto enforces source-address against the connection.
		CriticalOptions: authed.CriticalOptions,
		Extensions: map[string]string{
			"user_id":  user.ID,
			"user":     user.Name,
			"pubkey":   authed.Pubkey,
			"identity": authed.Identity,
		},
	}

	if impID != "" {
		perms.Extensions["imp_id"] = impID
	}

	return perms, nil
}
