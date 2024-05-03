package pico

import (
	"fmt"
	"log/slog"
	"strings"

	"git.sr.ht/~delthas/senpai"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/send/send/utils"
)

func getUser(s ssh.Session, dbpool db.DB) (*db.User, error) {
	var err error
	key, err := shared.KeyText(s)
	if err != nil {
		return nil, fmt.Errorf("key not found")
	}

	user, err := dbpool.FindUserForKey(s.User(), key)
	if err != nil {
		return nil, err
	}

	if user.Name == "" {
		return nil, fmt.Errorf("must have username set")
	}

	return user, nil
}

type Cmd struct {
	User    *db.User
	Session shared.CmdSession
	Log     *slog.Logger
	Dbpool  db.DB
	Write   bool
	Styles  common.Styles
}

func (c *Cmd) output(out string) {
	_, _ = c.Session.Write([]byte(out + "\r\n"))
}

func (c *Cmd) help() {
	helpStr := "Commands: [help, pico+]\n"
	c.output(helpStr)
}

func (c *Cmd) plus() {
	clientRefId := c.User.Name
	paymentLink := "https://buy.stripe.com/6oEaIvaNq7DA4NO9AD"
	url := fmt.Sprintf("%s?client_reference_id=%s", paymentLink, clientRefId)
	md := fmt.Sprintf(`# pico+

Signup to get premium access

## $2/month (billed annually)

Includes:
- pgs.sh - 10GB asset storage
- tuns.sh - full access
- imgs.sh - 5GB image registry storage
- prose.sh - 1GB image storage
- prose.sh - 1GB image storage
- beta access - Invited to join our private IRC channel

There are a few ways to purchase a membership. We try our best to
provide immediate access to pico+ regardless of payment method.

## Stripe (US/CA Only)

%s

This is the quickest way to access pico+. The Stripe payment
method requires an email address. We will never use your email
for anything unless absolutely necessary.

## Snail Mail

Send cash (USD) or check to:
- pico.sh LLC
- 206 E Huron St STE 103
- Ann Arbor MI 48104

Message us when payment is in transit and we will grant you
temporary access topico+ that will be converted to a full
year after we received it.

## Notes

Have any questions not covered here? Email us or join IRC,
we will promptly respond.

Unfortunately we do not have the labor bandwidth to support
international users for pico+ at this time. As a result,
we only offer our premium services to the US and Canada.

We do not maintain active subscriptions for pico+. Every
year you must pay again. We do not take monthly payments,
you must pay for a year up-front. Pricing is subject to
change because we plan on continuing to include more services
as we build them.

Need higher limits? We are more than happy to extend limits.
Just message us and we can chat.
`, url)

	c.output(md)
}

type CliHandler struct {
	DBPool db.DB
	Logger *slog.Logger
}

type Vtty struct {
	ssh.Session
}

func (v Vtty) Drain() error {
	v.Write([]byte("\033c"))
	err := v.Exit(0)
	if err != nil {
		return err
	}
	err = v.Close()
	return err
}

func (v Vtty) Start() error {
	return nil
}

func (v Vtty) Stop() error {
	return nil
}

func (v Vtty) WindowSize() (width int, height int, err error) {
	pty, _, _ := v.Pty()
	return pty.Window.Width, pty.Window.Height, nil
}

func (v Vtty) NotifyResize(cb func()) {
	_, notify, _ := v.Pty()
	go func() {
		for range notify {
			cb()
		}
	}()
}

func WishMiddleware(handler *CliHandler) wish.Middleware {
	dbpool := handler.DBPool
	log := handler.Logger

	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {
			args := sesh.Command()
			if len(args) == 0 {
				next(sesh)
				return
			}

			user, err := getUser(sesh, dbpool)
			if err != nil {
				utils.ErrorHandler(sesh, err)
				return
			}

			if len(args) > 0 && args[0] == "chat" {
				_, _, hasPty := sesh.Pty()
				if !hasPty {
					wish.Fatalln(sesh, "need pty `-t`")
					return
				}

				chatToken, _ := dbpool.FindTokenByName(user.ID, "pico-chat")
				if chatToken == "" {
					chatToken, err = dbpool.InsertToken(user.ID, "pico-chat")
					if err != nil {
						wish.Error(sesh, err)
						return
					}
				}
				vty := Vtty{
					sesh,
				}
				senpaiCfg := senpai.Defaults()
				senpaiCfg.TLS = true
				senpaiCfg.Addr = "irc.pico.sh:6697"
				senpaiCfg.Nick = user.Name
				senpaiCfg.Password = &chatToken
				senpaiCfg.Tty = vty

				app, err := senpai.NewApp(senpaiCfg)
				if err != nil {
					wish.Error(sesh, err)
					return
				}

				app.Run()
				app.Close()
				return
			}

			opts := Cmd{
				Session: sesh,
				User:    user,
				Log:     log,
				Dbpool:  dbpool,
				Write:   false,
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				if cmd == "help" {
					opts.help()
					return
				} else if cmd == "pico+" {
					opts.plus()
					return
				} else {
					next(sesh)
					return
				}
			}

			next(sesh)
		}
	}
}
