package tui

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/picosh/pico/db"
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
		styles := common.DefaultStyles(renderer)

		shrd := common.SharedModel{
			Session: sesh,
			Cfg:     cfg,
			Dbpool:  dbpool,
			Styles:  styles,
			Width:   80,
			Height:  24,
		}

		m := NewUI(shrd)

		user, err := findUser(shrd)
		if err != nil {
			wish.Errorln(sesh, err)
			return nil, nil
		}
		m.shared.User = user

		ff, _ := findPlusFeatureFlag(shrd)
		m.shared.PlusFeatureFlag = ff

		opts := bm.MakeOptions(sesh)
		opts = append(opts, tea.WithAltScreen())
		return m, opts
	}
}

func findUser(shrd common.SharedModel) (*db.User, error) {
	logger := shrd.Cfg.Logger
	var user *db.User
	usr := shrd.Session.User()

	key, err := shared.KeyForKeyText(shrd.Session.PublicKey())
	if err != nil {
		return nil, err
	}

	user, err = shrd.Dbpool.FindUserForKey(usr, key)
	if err != nil {
		logger.Error("no user found for public key", "err", err.Error())
		// we only want to throw an error for specific cases
		if errors.Is(err, &db.ErrMultiplePublicKeys{}) {
			return nil, err
		}
		// no user and not error indicates we need to create an account
		return nil, nil
	}

	return user, nil
}

func findPlusFeatureFlag(shrd common.SharedModel) (*db.FeatureFlag, error) {
	if shrd.User == nil {
		return nil, nil
	}

	ff, err := shrd.Dbpool.FindFeatureForUser(shrd.User.ID, "plus")
	if err != nil {
		return nil, err
	}

	return ff, nil
}
