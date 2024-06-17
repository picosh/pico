package shared

import "github.com/charmbracelet/ssh"

func PubkeyAuthHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	return true
}
