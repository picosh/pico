package pgs

import (
	"slices"

	"github.com/picosh/pico/pkg/db"
	"golang.org/x/crypto/ssh"
)

func HasProjectAccess(project *db.Project, owner *db.User, requester *db.User, pubkey ssh.PublicKey) bool {
	aclType := project.Acl.Type
	data := project.Acl.Data

	if aclType == "public" || aclType == "" {
		return true
	}

	if requester != nil && owner != nil && owner.ID == requester.ID {
		return true
	}

	switch aclType {
	case "pico":
		if requester == nil {
			return false
		}
		if len(data) == 0 {
			return true
		}
		return slices.Contains(data, requester.Name)
	case "pubkeys":
		if pubkey == nil {
			return false
		}
		if len(data) == 0 {
			return true
		}
		return slices.Contains(data, ssh.FingerprintSHA256(pubkey))
	}

	// private, http-pass, http-basic and unknown types are for the owner only.
	return false
}
