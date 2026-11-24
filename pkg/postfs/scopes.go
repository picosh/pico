package postfs

import (
	"path"

	"github.com/picosh/pico/pkg/send/scoped"
)

// Roles a cert limited to some posts may have.
const (
	RoleRead  = "read"
	RoleWrite = "write"
)

// ScopeRules limit a connection to the posts its scopes name by filename, or
// by the start of one with a trailing *.
var ScopeRules = scoped.Rules{
	Read:     []string{RoleRead, RoleWrite},
	Write:    []string{RoleWrite},
	Resource: path.Base,
}
