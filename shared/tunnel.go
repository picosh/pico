package shared

import (
	"fmt"

	"github.com/charmbracelet/ssh"
)

type ctxPublicKey struct{}

func GetPublicKeyCtx(ctx ssh.Context) (ssh.PublicKey, error) {
	pk, ok := ctx.Value(ctxPublicKey{}).(ssh.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key not set on `ssh.Context()` for connection")
	}
	return pk, nil
}
func SetPublicKeyCtx(ctx ssh.Context, pk ssh.PublicKey) {
	ctx.SetValue(ctxPublicKey{}, pk)
}
