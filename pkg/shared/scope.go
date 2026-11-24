package shared

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"unicode"

	"github.com/picosh/pico/pkg/pssh"
	"golang.org/x/crypto/ssh"
)

// Scope is one thing a cert allows on a service, from a principal of the form
// <service>:<role>[:<resource>]. Without a resource it covers everything the
// account reaches. Each service has its own roles and resources.
type Scope struct {
	Role     string `json:"role"`
	Resource string `json:"resource,omitempty"`
}

func (s Scope) String() string {
	if s.Resource == "" {
		return s.Role
	}
	return s.Role + " " + s.Resource
}

// covers reports whether the scope reaches resource. A resource ending in *
// covers names starting with what's before it. The empty resource is the
// account as a whole, which only a scope without one covers.
func (s Scope) covers(resource string) bool {
	if s.Resource == "" {
		return true
	}
	if prefix, ok := strings.CutSuffix(s.Resource, "*"); ok && resource != "" {
		return strings.HasPrefix(resource, prefix)
	}
	return s.Resource == resource
}

// Scopes are a connection's scopes. Nil doesn't limit it, and an empty list
// allows nothing.
type Scopes []Scope

const scopesExtension = "scopes"

// SessionScopes returns the scopes a connection authenticated with.
func SessionScopes(s *pssh.SSHServerConnSession) Scopes {
	if perms := s.Permissions(); perms != nil {
		return permissionScopes(perms)
	}
	return nil
}

func permissionScopes(perms *ssh.Permissions) Scopes {
	encoded, ok := perms.Extensions[scopesExtension]
	if !ok {
		return nil
	}

	var scopes Scopes
	if err := json.Unmarshal([]byte(encoded), &scopes); err != nil || scopes == nil {
		return Scopes{}
	}
	return scopes
}

// Limited reports whether the connection is limited to its scopes.
func (s Scopes) Limited() bool { return s != nil }

// Allows reports whether the connection has one of roles on resource, or on
// the account as a whole if resource is empty.
func (s Scopes) Allows(resource string, roles ...string) bool {
	if !s.Limited() {
		return true
	}
	return slices.ContainsFunc(s, func(scope Scope) bool {
		return slices.Contains(roles, scope.Role) && scope.covers(resource)
	})
}

// Check is Allows as an error naming what was refused and what the
// certificate allows.
func (s Scopes) Check(action, resource string, roles ...string) error {
	if s.Allows(resource, roles...) {
		return nil
	}
	return s.denied(strings.TrimSpace(action + " " + resource))
}

// CheckFull refuses an action no scope allows.
func (s Scopes) CheckFull(action string) error {
	if !s.Limited() {
		return nil
	}
	return s.denied(action)
}

func (s Scopes) denied(action string) error {
	allows := make([]string, 0, len(s))
	for _, scope := range s {
		allows = append(allows, scope.String())
	}
	return fmt.Errorf("%w: this certificate doesn't allow %s. It allows: %s", fs.ErrPermission, action, strings.Join(allows, ", "))
}

// PubkeyCertVerifyScoped is PubkeyCertVerify for callers that enforce scopes:
// it also accepts a cert limited to part of the service, and returns its
// scopes. A cert that names the service or admin isn't limited.
func PubkeyCertVerifyScoped(key ssh.PublicKey, srcPrincipal string) (*AuthedPubkey, error) {
	cert, ok := key.(*ssh.Certificate)
	if !ok || srcPrincipal == "" || certPrincipal(cert, srcPrincipal) != "" {
		return PubkeyCertVerify(key, srcPrincipal)
	}

	scopes, principal, err := parseScopes(cert, srcPrincipal)
	if err != nil {
		return nil, err
	}

	authed, err := verifyCert(cert, principal)
	if err != nil {
		return nil, err
	}

	authed.Scopes = scopes
	return authed, nil
}

// parseScopes returns a cert's scopes on a service, and one of the principals
// they came from.
func parseScopes(cert *ssh.Certificate, service string) (Scopes, string, error) {
	var scopes Scopes
	principal := ""

	for _, princ := range cert.ValidPrincipals {
		rest, ok := strings.CutPrefix(princ, service+":")
		if !ok {
			continue
		}

		role, resource, hasResource := strings.Cut(rest, ":")
		if role == "" || (hasResource && resource == "") || strings.ContainsFunc(princ, func(r rune) bool { return !unicode.IsPrint(r) }) {
			return nil, "", fmt.Errorf("ssh-cert scope %q not valid", princ)
		}

		scopes = append(scopes, Scope{Role: role, Resource: resource})
		principal = princ
	}

	if len(scopes) == 0 {
		return nil, "", fmt.Errorf("ssh-cert principals not valid")
	}

	return scopes, principal, nil
}

// certPrincipal returns the principal that gives a cert full access to a
// service, if it has one.
func certPrincipal(cert *ssh.Certificate, service string) string {
	i := slices.IndexFunc(cert.ValidPrincipals, func(princ string) bool {
		return princ == "admin" || princ == service
	})
	if i < 0 {
		return ""
	}

	return cert.ValidPrincipals[i]
}
