package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/muesli/termenv"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui/common"
)

func CmsMiddleware(cfg *shared.ConfigSite) bm.Handler {
	return func(sesh ssh.Session) (tea.Model, []tea.ProgramOption) {
		logger := cfg.Logger

		_, _, active := sesh.Pty()
		if !active {
			logger.Info("no active terminal, skipping")
			return nil, nil
		}

		dbpool := postgres.NewDB(cfg.DbURL, cfg.Logger)
		renderer := bm.MakeRenderer(sesh)
		renderer.SetColorProfile(termenv.TrueColor)
		styles := common.DefaultStyles(renderer)

		shrd := &common.SharedModel{
			Session: sesh,
			Cfg:     cfg,
			Dbpool:  dbpool,
			Styles:  styles,
			Width:   80,
			Height:  24,
			Logger:  logger,
		}

		m := NewUI(shrd)
		err := m.setupUser()
		if err != nil {
			return nil, nil
		}

		opts := bm.MakeOptions(sesh)
		opts = append(opts, tea.WithAltScreen())
		return m, opts
	}
}
