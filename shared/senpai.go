package shared

import (
	"fmt"
	"log/slog"
	"sync"

	"git.sr.ht/~delthas/senpai"
	"github.com/charmbracelet/ssh"
	"github.com/containerd/console"
)

type consoleData struct {
	data []byte
	err  error
}

type VConsole struct {
	ssh.Session
	pty ssh.Pty

	sizeEnableOnce sync.Once

	windowMu      sync.Mutex
	currentWindow ssh.Window

	readReq  chan []byte
	dataChan chan consoleData

	wg sync.WaitGroup
}

func (v *VConsole) Read(p []byte) (int, error) {
	v.wg.Add(1)
	defer v.wg.Done()
	tot := 0

	if len(v.readReq) == 0 {
		select {
		case v.readReq <- make([]byte, len(p)):
		case <-v.Session.Context().Done():
			return 0, v.Session.Context().Err()
		}
	}

	v.sizeEnableOnce.Do(func() {
		tot += copy(p, []byte("\x1b[?2048h"))
		v.windowMu.Lock()
		select {
		case v.dataChan <- consoleData{[]byte(fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", v.currentWindow.Height, v.currentWindow.Width, v.currentWindow.HeightPixels, v.currentWindow.WidthPixels)), nil}:
		case <-v.Session.Context().Done():
			return
		}
		v.windowMu.Unlock()
		select {
		case v.dataChan <- consoleData{[]byte("\x1b[?1;0c"), nil}:
		case <-v.Session.Context().Done():
			return
		}
	})

	if tot > 0 {
		return tot, nil
	}

	select {
	case data := <-v.dataChan:
		tot += copy(p, data.data)
		return tot, data.err
	case <-v.Session.Context().Done():
		return 0, v.Session.Context().Err()
	}
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

func (v *VConsole) Close() error {
	err := v.Session.Close()
	v.wg.Wait()
	close(v.readReq)
	close(v.dataChan)
	return err
}

func NewVConsole(sesh ssh.Session) (*VConsole, error) {
	pty, win, ok := sesh.Pty()
	if !ok {
		return nil, fmt.Errorf("PTY not found")
	}

	vty := &VConsole{
		pty:           pty,
		Session:       sesh,
		currentWindow: pty.Window,
		readReq:       make(chan []byte, 100),
		dataChan:      make(chan consoleData, 100),
	}

	vty.wg.Add(2)

	go func() {
		defer vty.wg.Done()
		for {
			select {
			case <-sesh.Context().Done():
				return
			case data := <-vty.readReq:
				n, err := vty.Session.Read(data)
				select {
				case vty.dataChan <- consoleData{data[:n], err}:
				case <-sesh.Context().Done():
					return
				}
			}
		}
	}()

	go func() {
		defer vty.wg.Done()
		for {
			select {
			case <-sesh.Context().Done():
				return
			case w := <-win:
				vty.windowMu.Lock()
				vty.currentWindow = w
				vty.windowMu.Unlock()
				select {
				case vty.dataChan <- consoleData{[]byte(fmt.Sprintf("\x1b[48;%d;%d;%d;%dt", w.Height, w.Width, w.HeightPixels, w.WidthPixels)), nil}:
				case <-sesh.Context().Done():
					return
				}
			}
		}
	}()

	return vty, nil
}

func NewSenpaiApp(sesh ssh.Session, username, pass string) (*senpai.App, error) {
	vty, err := NewVConsole(sesh)
	if err != nil {
		slog.Error("PTY not found")
		return nil, err
	}

	senpaiCfg := senpai.Defaults()
	senpaiCfg.TLS = true
	senpaiCfg.Addr = "irc.pico.sh:6697"
	senpaiCfg.Nick = username
	senpaiCfg.Password = &pass
	senpaiCfg.WithConsole = vty

	app, err := senpai.NewApp(senpaiCfg)
	return app, err
}
