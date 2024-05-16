package tui

import (
	"io"

	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui/common"
)

type SenpaiCmd struct {
	shared common.SharedModel
}

func (m *SenpaiCmd) Run() error {
	pass, err := m.shared.Dbpool.UpsertToken(m.shared.User.ID, "pico-chat")
	if err != nil {
		return err
	}
	app, err := shared.NewSenpaiApp(m.shared.Session, m.shared.User.Name, pass)
	if err != nil {
		return err
	}
	app.Run()
	app.Close()
	return nil
}

func (m *SenpaiCmd) SetStdin(io.Reader)  {}
func (m *SenpaiCmd) SetStdout(io.Writer) {}
func (m *SenpaiCmd) SetStderr(io.Writer) {}
