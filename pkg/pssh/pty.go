package pssh

import (
	"encoding/binary"
	"fmt"
)

func SessionMessage(sesh *SSHServerConnSession, msg string) {
	_, _ = sesh.Write([]byte(msg + "\r\n"))
}

func DeprecatedNotice() SSHServerMiddleware {
	return func(next SSHServerHandler) SSHServerHandler {
		return func(sesh *SSHServerConnSession) error {
			msg := fmt.Sprintf(
				"%s\r\n\r\nRun %s to access pico's TUI",
				"DEPRECATED",
				"ssh pico.sh",
			)
			SessionMessage(sesh, msg)
			return next(sesh)
		}
	}
}

func PtyMdw(mdw SSHServerMiddleware) SSHServerMiddleware {
	return func(next SSHServerHandler) SSHServerHandler {
		return func(sesh *SSHServerConnSession) error {
			_, _, ok := sesh.Pty()
			if !ok {
				return next(sesh)
			}
			return mdw(next)(sesh)
		}
	}
}

type Window struct {
	Width        int
	Height       int
	HeightPixels int
	WidthPixels  int
}

type Pty struct {
	Term   string
	Window Window
}

func (p *Pty) Resize(width, height int) error {
	return nil
}

func (p *Pty) Name() string {
	return ""
}

func parsePtyRequest(s []byte) (pty Pty, ok bool) {
	term, s, ok := parseString(s)
	if !ok {
		return
	}
	width32, s, ok := parseUint32(s)
	if !ok {
		return
	}
	height32, _, ok := parseUint32(s)
	if !ok {
		return
	}
	pty = Pty{
		Term: term,
		Window: Window{
			Width:  int(width32),
			Height: int(height32),
		},
	}
	return
}

func parseWinchRequest(s []byte) (win Window, ok bool) {
	width32, s, ok := parseUint32(s)
	if width32 < 1 {
		ok = false
	}
	if !ok {
		return
	}
	height32, _, ok := parseUint32(s)
	if height32 < 1 {
		ok = false
	}
	if !ok {
		return
	}
	win = Window{
		Width:  int(width32),
		Height: int(height32),
	}
	return
}

func parseString(in []byte) (out string, rest []byte, ok bool) {
	if len(in) < 4 {
		return
	}
	length := binary.BigEndian.Uint32(in)
	if uint32(len(in)) < 4+length {
		return
	}
	out = string(in[4 : 4+length])
	rest = in[4+length:]
	ok = true
	return
}

func parseUint32(in []byte) (uint32, []byte, bool) {
	if len(in) < 4 {
		return 0, nil, false
	}
	return binary.BigEndian.Uint32(in), in[4:], true
}
