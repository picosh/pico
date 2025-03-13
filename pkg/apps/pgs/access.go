package pgs

import (
	"slices"

	"github.com/picosh/pico/pkg/db"
	"golang.org/x/crypto/ssh"
)

func HasProjectAccess(project *db.Project, owner *db.User, requester *db.User, pubkey ssh.PublicKey) bool {
	aclType := project.Acl.Type
	data := project.Acl.Data

	if aclType == "public" {
		return true
	}

	if requester != nil {
		if owner.ID == requester.ID {
			return true
		}
	}

	if aclType == "pico" {
		if requester == nil {
			return false
		}

		if len(data) == 0 {
			return true
		}
		return slices.Contains(data, requester.Name)
	}

	if aclType == "pubkeys" {
		key := ssh.FingerprintSHA256(pubkey)
		if len(data) == 0 {
			return true
		}
		return slices.Contains(data, key)
	}

	return true
}
