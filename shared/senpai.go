package shared

import (
	"git.sr.ht/~delthas/senpai"
	"github.com/charmbracelet/ssh"
)

type Vtty struct {
	ssh.Session
}

func (v Vtty) Drain() error {
	_, err := v.Write([]byte("\033[?25h\033[0 q\033[34h\033[?25h\033[39;49m\033[m^O\033[H\033[J\033[?1049l\033[?1l\033>\033[?1000l\033[?1002l\033[?1003l\033[?1006l\033[?2004l"))
	if err != nil {
		return err
	}

	err = v.Exit(0)
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

func NewSenpaiApp(sesh ssh.Session, username, pass string) (*senpai.App, error) {
	vty := Vtty{
		sesh,
	}
	senpaiCfg := senpai.Defaults()
	senpaiCfg.TLS = true
	senpaiCfg.Addr = "irc.pico.sh:6697"
	senpaiCfg.Nick = username
	senpaiCfg.Password = &pass
	senpaiCfg.Tty = vty

	app, err := senpai.NewApp(senpaiCfg)
	return app, err
}
