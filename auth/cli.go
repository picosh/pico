package auth

import (
	"fmt"
	"log/slog"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
)

type Cmd struct {
	User    *db.User
	Session shared.CmdSession
	Log     *slog.Logger
	Store   storage.StorageServe
	Dbpool  db.DB
	Write   bool
}

func (c *Cmd) output(out string) {
	_, _ = c.Session.Write([]byte(out + "\r\n"))
}

func (c *Cmd) error(err error) {
	_, _ = fmt.Fprint(c.Session.Stderr(), err, "\r\n")
	_ = c.Session.Exit(1)
	_ = c.Session.Close()
}

func (c *Cmd) bail(err error) {
	if err == nil {
		return
	}
	c.Log.Error(err.Error())
	c.error(err)
}

func (c *Cmd) help() {}
func (c *Cmd) register() error {
	return nil
}
