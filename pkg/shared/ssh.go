package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/picosh/pico/pkg/db"
	"golang.org/x/crypto/ssh"
)

const adminPrefix = "admin__"

type SshAuthHandler struct {
	DB        db.AuthDB
	Logger    *slog.Logger
	Principal string
	// ScopeRoles are the roles a cert limited to part of the service may
	// have. Without any, such certs are refused.
	ScopeRoles []string
}

// NewSshAuthHandler authenticates a service's users. Services that enforce
// SessionScopes name the roles scopes may have.
func NewSshAuthHandler(dbh db.AuthDB, logger *slog.Logger, principal string, scopeRoles ...string) *SshAuthHandler {
	return &SshAuthHandler{
		DB:         dbh,
		Logger:     logger,
		Principal:  principal,
		ScopeRoles: scopeRoles,
	}
}

// verify checks key, accepting scoped certs if the service enforces them.
func (r *SshAuthHandler) verify(conn ssh.ConnMetadata, key ssh.PublicKey) (*AuthedPubkey, error) {
	if len(r.ScopeRoles) == 0 {
		return PubkeyCertVerify(key, r.Principal)
	}

	authed, err := PubkeyCertVerifyScoped(key, r.Principal)
	if err != nil {
		return nil, err
	}
	if !authed.Scopes.Limited() {
		return authed, nil
	}

	for _, scope := range authed.Scopes {
		if !slices.Contains(r.ScopeRoles, scope.Role) {
			return nil, fmt.Errorf("ssh-cert scope role %q not valid, use %s", scope.Role, strings.Join(r.ScopeRoles, " or "))
		}
	}
	if strings.HasPrefix(conn.User(), adminPrefix) {
		return nil, fmt.Errorf("ssh-cert with scopes can't start admin sessions")
	}
	return authed, nil
}

type AuthedPubkey struct {
	OrigPubkey string
	Pubkey     string
	Identity   string
	// CriticalOptions holds a cert's source-address restriction, which the
	// caller enforces against the client's address.
	CriticalOptions map[string]string
	// Scopes limit a cert to part of the service. Nil doesn't.
	Scopes Scopes
	// Cert is nil for a plain key.
	Cert *ssh.Certificate
}

const sourceAddressOption = "source-address"

// certChecker verifies a cert's CA signature, validity window and critical
// options.
var certChecker = &ssh.CertChecker{
	SupportedCriticalOptions: []string{sourceAddressOption},
}

// PubkeyCertVerify returns the account key behind key: for a cert, the CA key
// that signed it. The cert must name the service or admin.
func PubkeyCertVerify(key ssh.PublicKey, srcPrincipal string) (*AuthedPubkey, error) {
	cert, ok := key.(*ssh.Certificate)
	if !ok {
		origPubkey := KeyForKeyText(key)
		return &AuthedPubkey{
			OrigPubkey: origPubkey,
			Pubkey:     origPubkey,
			Identity:   "pubkey",
		}, nil
	}

	principal := certPrincipal(cert, srcPrincipal)
	if principal == "" {
		return nil, fmt.Errorf("ssh-cert principals not valid")
	}

	return verifyCert(cert, principal)
}

func verifyCert(cert *ssh.Certificate, principal string) (*AuthedPubkey, error) {
	if cert.CertType != ssh.UserCert {
		return nil, fmt.Errorf("ssh-cert has type %d", cert.CertType)
	}

	// The account is found by the CA key, so the cert must be signed by it.
	if err := certChecker.CheckCert(principal, cert); err != nil {
		return nil, fmt.Errorf("ssh-cert invalid: %w", err)
	}

	return &AuthedPubkey{
		OrigPubkey:      KeyForKeyText(cert),
		Pubkey:          KeyForKeyText(cert.SignatureKey),
		Identity:        cert.KeyId,
		CriticalOptions: cert.CriticalOptions,
		Cert:            cert,
	}, nil
}

// ParseCert reads an authorized_keys line holding a cert, and checks what
// verifyCert does for any of its principals, except that the cert may have
// expired or not be valid yet.
func ParseCert(line string) (*ssh.Certificate, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return nil, err
	}

	cert, ok := key.(*ssh.Certificate)
	if !ok || len(cert.ValidPrincipals) == 0 {
		return nil, fmt.Errorf("not an ssh-cert with principals")
	}

	if cert.CertType != ssh.UserCert {
		return nil, fmt.Errorf("ssh-cert has type %d", cert.CertType)
	}

	checker := *certChecker
	checker.Clock = func() time.Time { return time.Unix(int64(cert.ValidAfter), 0) }
	if err := checker.CheckCert(cert.ValidPrincipals[0], cert); err != nil {
		return nil, fmt.Errorf("ssh-cert invalid: %w", err)
	}
	return cert, nil
}

// ErrCertRevoked means the account whose CA signed a cert revoked it.
var ErrCertRevoked = errors.New("ssh-cert is revoked")

// CheckRevoked refuses a cert revoked by the account whose CA signed it.
func (a *AuthedPubkey) CheckRevoked(dbpool interface {
	IsCertRevoked(userID, fingerprint, keyID string) (bool, error)
}, userID string) error {
	if a.Cert == nil {
		return nil
	}

	keyID := a.Cert.KeyId
	if !RevocableKeyID(keyID) {
		// No entry can name it, and postgres refuses to look for it.
		keyID = ""
	}

	revoked, err := dbpool.IsCertRevoked(userID, ssh.FingerprintSHA256(a.Cert.Key), keyID)
	if err != nil {
		return fmt.Errorf("cannot check if ssh-cert is revoked: %w", err)
	}
	if revoked {
		return ErrCertRevoked
	}
	return nil
}

// maxKeyIDLen keeps entries under postgres's limit on indexed values.
const maxKeyIDLen = 1024

// RevocableKeyID reports whether an entry can name a KeyId, which is any
// bytes the CA chose.
func RevocableKeyID(keyID string) bool {
	return keyID != "" && len(keyID) <= maxKeyIDLen && utf8.ValidString(keyID) && !strings.Contains(keyID, "\x00")
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
	authed, err := r.verify(conn, key)
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

	if err := authed.CheckRevoked(r.DB, user.ID); err != nil {
		log.Error("ssh-cert refused", "user", user.Name, "identity", authed.Identity, "err", err)
		return nil, err
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

	if authed.Scopes.Limited() {
		scopes, err := json.Marshal(authed.Scopes)
		if err != nil {
			return nil, err
		}
		perms.Extensions[scopesExtension] = string(scopes)
	}

	if impID != "" {
		perms.Extensions["imp_id"] = impID
	}

	return perms, nil
}

// OpenPubkeyAuthHandler is PubkeyAuthHandler for services that let in keys
// without an account. A cert that failed its checks is refused, so it isn't
// taken for an unknown key.
func (r *SshAuthHandler) OpenPubkeyAuthHandler(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	perms, err := r.PubkeyAuthHandler(conn, key)
	if err == nil {
		return perms, nil
	}

	r.Logger.Warn("pubkey auth handler", "err", err)
	if _, isCert := key.(*ssh.Certificate); isCert {
		return nil, err
	}

	return &ssh.Permissions{
		Extensions: map[string]string{
			"pubkey": KeyForKeyText(key),
		},
	}, nil
}
