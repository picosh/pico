package shared

import (
	"fmt"
	"log/slog"
	"sync"

	"git.sr.ht/~delthas/senpai"
	"github.com/charmbracelet/ssh"
	"github.com/containerd/console"
)

type VConsole struct {
	ssh.Session
	pty ssh.Pty

	sizeEnableOnce  sync.Once
	primaryAttrOnce sync.Once

	sendSizeMu sync.Mutex
	sendSize   bool

	windowMu      sync.Mutex
	currentWindow ssh.Window
}

func (v *VConsole) Read(p []byte) (int, error) {
	tot := 0

	v.sizeEnableOnce.Do(func() {
		tot += copy(p, []byte("\x1b[?2048h"))
		v.sendSizeMu.Lock()
		v.sendSize = true
		v.sendSizeMu.Unlock()
	})

	if tot > 0 {
		return tot, nil
	}

	ok := v.sendSizeMu.TryLock()
	if ok {
		if v.sendSize {
			v.sendSize = false

			v.windowMu.Lock()
			tot += copy(p, []byte(fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", v.currentWindow.Height, v.currentWindow.Width, v.currentWindow.HeightPixels, v.currentWindow.WidthPixels)))
			v.windowMu.Unlock()
		}

		v.sendSizeMu.Unlock()
	}

	if tot > 0 {
		return tot, nil
	}

	v.primaryAttrOnce.Do(func() {
		tot += copy(p, []byte("\x1b[?1;0c"))
	})

	if tot > 0 {
		return tot, nil
	}

	n, err := v.Session.Read(p)
	return tot + n, err
}

func (v *VConsole) Resize(winSize console.WinSize) error {
	return v.pty.Resize(int(winSize.Height), int(winSize.Width))
}

func (v *VConsole) ResizeFrom(c console.Console) error {
	s, err := c.Size()
	if err != nil {
		return err
	}

	return v.Resize(s)
}

func (v *VConsole) SetRaw() error {
	return nil
}

func (v *VConsole) DisableEcho() error {
	return nil
}

func (v *VConsole) Reset() error {
	_, err := v.Write([]byte("\033[?25h\033[0 q\033[34h\033[?25h\033[39;49m\033[m^O\033[H\033[J\033[?1049l\033[?1l\033>\033[?1000l\033[?1002l\033[?1003l\033[?1006l\033[?2004l"))
	return err
}

func (v *VConsole) Size() (console.WinSize, error) {
	v.windowMu.Lock()
	defer v.windowMu.Unlock()
	return console.WinSize{
		Height: uint16(v.currentWindow.Height),
		Width:  uint16(v.currentWindow.Width),
	}, nil
}

func (v *VConsole) Fd() uintptr {
	return v.pty.Slave.Fd()
}

func (v *VConsole) Name() string {
	return v.pty.Name()
}

func NewSenpaiApp(sesh ssh.Session, username, pass string) (*senpai.App, error) {
	pty, win, ok := sesh.Pty()
	if !ok {
		slog.Error("PTY not found")
		return nil, fmt.Errorf("PTY not found")
	}

	vty := &VConsole{
		pty:           pty,
		Session:       sesh,
		currentWindow: pty.Window,
	}

	go func() {
		for w := range win {
			vty.windowMu.Lock()
			vty.currentWindow = w
			vty.windowMu.Unlock()

			vty.sendSizeMu.Lock()
			vty.sendSize = true
			vty.sendSizeMu.Unlock()
		}
	}()

	senpaiCfg := senpai.Defaults()
	senpaiCfg.TLS = true
	senpaiCfg.Addr = "irc.pico.sh:6697"
	senpaiCfg.Nick = username
	senpaiCfg.Password = &pass
	senpaiCfg.WithConsole = vty

	app, err := senpai.NewApp(senpaiCfg)
	return app, err
}
