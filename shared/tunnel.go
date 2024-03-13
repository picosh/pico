package shared

import (
	"fmt"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
)

type ctxPublicKey struct{}
type ctxUserKey struct{}

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

func GetUserCtx(ctx ssh.Context) (*db.User, error) {
	pk, ok := ctx.Value(ctxUserKey{}).(*db.User)
	if !ok {
		return nil, fmt.Errorf("user not set on `ssh.Context()` for connection")
	}
	return pk, nil
}

func SetUserCtx(ctx ssh.Context, user *db.User) {
	ctx.SetValue(ctxUserKey{}, user)
}
