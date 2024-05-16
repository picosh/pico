package common

import (
	"log/slog"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
)

type SharedModel struct {
	Logger          *slog.Logger
	Session         ssh.Session
	Cfg             *shared.ConfigSite
	Dbpool          db.DB
	User            *db.User
	PlusFeatureFlag *db.FeatureFlag
	Width           int
	Height          int
	Styles          Styles
}

type ExitMsg struct{}

func ExitPage() tea.Cmd {
	return func() tea.Msg {
		return ExitMsg{}
	}
}
