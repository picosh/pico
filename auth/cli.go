package auth

import (
	"fmt"
	"log/slog"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
)

type Cmd struct {
	Username  string
	PublicKey string
	User      *db.User
	Session   shared.CmdSession
	Log       *slog.Logger
	Store     storage.StorageServe
	Dbpool    db.DB
	Write     bool
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

func (c *Cmd) help() {
	if c.User == nil {
		helpStr := "commands: [help, register]"
		c.output(helpStr)
	}
	c.output("commands: [help]")
}

func (c *Cmd) registerUser(username string) (*db.User, error) {
	userID, err := c.Dbpool.AddUser()
	if err != nil {
		return nil, err
	}

	err = c.Dbpool.LinkUserKey(userID, c.PublicKey)
	if err != nil {
		return nil, err
	}

	err = c.Dbpool.SetUserName(userID, username)
	if err != nil {
		return nil, err
	}

	user, err := c.Dbpool.FindUser(userID)
	if err != nil {
		return nil, err
	}

	return user, nil
}

func (c *Cmd) register(username string) error {
	if c.User != nil {
		c.output("You already have an account")
		return nil
	}
	user, err := c.registerUser(username)
	if err != nil {
		return err
	}
	c.output(fmt.Sprintf("%s, you have successfully created an account!", user.Name))
	return nil
}
