package tui

import (
	"io"

	"github.com/picosh/pico/pkg/shared"
)

type SenpaiCmd struct {
	Shared *SharedModel
}

func (m *SenpaiCmd) Run() error {
	pass, err := m.Shared.Dbpool.UpsertToken(m.Shared.User.ID, "pico-chat")
	if err != nil {
		return err
	}
	app, err := shared.NewSenpaiApp(m.Shared.Session, m.Shared.User.Name, pass)
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
