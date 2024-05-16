package tui

import (
	"io"

	tea "github.com/charmbracelet/bubbletea"
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

func LoadChat(shrd common.SharedModel) tea.Cmd {
	sp := &SenpaiCmd{
		shared: shrd,
	}
	return tea.Exec(sp, func(err error) tea.Msg {
		return tea.Quit()
	})
}
