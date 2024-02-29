package info

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/wish/cms/config"
	"github.com/picosh/pico/wish/cms/ui/common"
)

type errMsg struct {
	err error
}

// Error satisfies the error interface.
func (e errMsg) Error() string {
	return e.err.Error()
}

// Model stores the state of the info user interface.
type Model struct {
	cfg             *config.ConfigCms
	urls            config.ConfigURL
	Quit            bool // signals it's time to exit the whole application
	Err             error
	User            *db.User
	PlusFeatureFlag *db.FeatureFlag
	styles          common.Styles
}

// NewModel returns a new Model in its initial state.
func NewModel(styles common.Styles, cfg *config.ConfigCms, urls config.ConfigURL, user *db.User, ff *db.FeatureFlag) Model {
	return Model{
		Quit:            false,
		User:            user,
		styles:          styles,
		cfg:             cfg,
		urls:            urls,
		PlusFeatureFlag: ff,
	}
}

// Update is the Bubble Tea update loop.
func Update(msg tea.Msg, m Model) (Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.Quit = true
			return m, nil
		}
	case errMsg:
		// If there's an error we print the error and exit
		m.Err = msg
		m.Quit = true
		return m, nil
	}

	return m, cmd
}

// View renders the current view from the model.
func (m Model) View() string {
	if m.Err != nil {
		return "error: " + m.Err.Error()
	} else if m.User == nil {
		return " Authenticating..."
	}
	return m.bioView()
}

func (m Model) bioView() string {
	var username string
	if m.User.Name != "" {
		username = m.User.Name
	} else {
		username = m.styles.Subtle.Render("(none set)")
	}

	plus := "No"
	expires := ""
	if m.PlusFeatureFlag != nil {
		plus = "Yes"
		expires = m.PlusFeatureFlag.ExpiresAt.Format("02 Jan 2006")
	}

	vals := []string{
		"Username", username,
		"Joined", m.User.CreatedAt.Format("02 Jan 2006"),
		"Pico+", plus,
	}

	if expires != "" {
		vals = append(vals, "Pico+ Expires At", expires)
	}

	return common.KeyValueView(m.styles, vals...)
}
