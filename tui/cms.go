package tui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/ssh"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/muesli/reflow/indent"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/reflow/wrap"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui/account"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/info"
	"github.com/picosh/pico/tui/keys"
	"github.com/picosh/pico/tui/tokens"
)

type status int

const (
	statusInit status = iota
	statusReady
	statusNoAccount
	statusBrowsingKeys
	statusBrowsingTokens
	statusQuitting
)

func (s status) String() string {
	return [...]string{
		"initializing",
		"ready",
		"browsing keys",
		"quitting",
		"error",
	}[s]
}

// menuChoice represents a chosen menu item.
type menuChoice int

// menu choices.
const (
	keysChoice menuChoice = iota
	tokensChoice
	exitChoice
	unsetChoice // set when no choice has been made
)

// menu text corresponding to menu choices. these are presented to the user.
var menuChoices = map[menuChoice]string{
	keysChoice:   "Manage keys",
	tokensChoice: "Manage tokens",
	exitChoice:   "Exit",
}

func NewSpinner(styles common.Styles) spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styles.Spinner
	return s
}

type GotDBMsg db.DB

func CmsMiddleware(cfg *shared.ConfigSite) bm.Handler {
	return func(s ssh.Session) (tea.Model, []tea.ProgramOption) {
		logger := cfg.Logger

		_, _, active := s.Pty()
		if !active {
			logger.Info("no active terminal, skipping")
			return nil, nil
		}

		sshUser := s.User()
		dbpool := postgres.NewDB(cfg.DbURL, cfg.Logger)
		renderer := lipgloss.NewRenderer(s)
		renderer.SetOutput(common.OutputFromSession(s))
		styles := common.DefaultStyles(renderer)

		m := model{
			cfg:        cfg,
			publicKey:  s.PublicKey(),
			dbpool:     dbpool,
			sshUser:    sshUser,
			status:     statusInit,
			menuChoice: unsetChoice,
			styles:     styles,
			spinner:    common.NewSpinner(styles),
			terminalSize: tea.WindowSizeMsg{
				Width:  80,
				Height: 24,
			},
		}

		user, err := m.findUser()
		if err != nil {
			_, _ = fmt.Fprintln(s.Stderr(), err)
			return nil, nil
		}
		m.user = user

		ff, _ := m.findPlusFeatureFlag()
		m.plusFeatureFlag = ff

		return m, []tea.ProgramOption{tea.WithAltScreen()}
	}
}

// Just a generic tea.Model to demo terminal information of ssh.
type model struct {
	cfg             *shared.ConfigSite
	publicKey       ssh.PublicKey
	dbpool          db.DB
	user            *db.User
	plusFeatureFlag *db.FeatureFlag
	err             error
	sshUser         string
	status          status
	menuIndex       int
	menuChoice      menuChoice
	styles          common.Styles
	info            info.Model
	spinner         spinner.Model
	keys            keys.Model
	tokens          tokens.Model
	createAccount   account.CreateModel
	terminalSize    tea.WindowSizeMsg
}

func (m model) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m model) findUser() (*db.User, error) {
	logger := m.cfg.Logger
	var user *db.User

	if m.sshUser == "new" {
		logger.Info("user requesting to register account")
		return nil, nil
	}

	key, err := shared.KeyForKeyText(m.publicKey)
	if err != nil {
		return nil, err
	}

	user, err = m.dbpool.FindUserForKey(m.sshUser, key)
	if err != nil {
		logger.Error("no user found for public key", "err", err.Error())
		// we only want to throw an error for specific cases
		if errors.Is(err, &db.ErrMultiplePublicKeys{}) {
			return nil, err
		}
		return nil, nil
	}

	return user, nil
}

func (m model) findPlusFeatureFlag() (*db.FeatureFlag, error) {
	if m.user == nil {
		return nil, nil
	}

	ff, err := m.dbpool.FindFeatureForUser(m.user.ID, "pgs")
	if err != nil {
		return nil, err
	}

	return ff, nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmds []tea.Cmd
		cmd  tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.terminalSize = msg
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.dbpool.Close()
			return m, tea.Quit
		}

		if m.status == statusReady { // Process keys for the menu
			switch msg.String() {
			// Quit
			case "q", "esc":
				m.status = statusQuitting
				m.dbpool.Close()
				return m, tea.Quit

			// Prev menu item
			case "up", "k":
				m.menuIndex--
				if m.menuIndex < 0 {
					m.menuIndex = len(menuChoices) - 1
				}

			// Select menu item
			case "enter":
				m.menuChoice = menuChoice(m.menuIndex)

			// Next menu item
			case "down", "j":
				m.menuIndex++
				if m.menuIndex >= len(menuChoices) {
					m.menuIndex = 0
				}
			}
		}
	case account.CreateAccountMsg:
		m.status = statusReady
		m.info.User = msg
		m.user = msg
		m.info = info.NewModel(m.styles, m.user, m.plusFeatureFlag)
		m.keys = keys.NewModel(m.styles, m.cfg.Logger, m.dbpool, m.user)
		m.tokens = tokens.NewModel(m.styles, m.dbpool, m.user)
		m.createAccount = account.NewCreateModel(m.styles, m.dbpool, m.publicKey)
	}

	switch m.status {
	case statusInit:
		m.info = info.NewModel(m.styles, m.user, m.plusFeatureFlag)
		m.keys = keys.NewModel(m.styles, m.cfg.Logger, m.dbpool, m.user)
		m.tokens = tokens.NewModel(m.styles, m.dbpool, m.user)
		m.createAccount = account.NewCreateModel(m.styles, m.dbpool, m.publicKey)
		if m.user == nil {
			m.status = statusNoAccount
		} else {
			m.status = statusReady
		}
	}

	m, cmd = updateChildren(msg, m)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func updateChildren(msg tea.Msg, m model) (model, tea.Cmd) {
	var cmd tea.Cmd

	switch m.status {
	case statusBrowsingKeys:
		newModel, newCmd := m.keys.Update(msg)
		keysModel, ok := newModel.(keys.Model)
		if !ok {
			panic("could not perform assertion on keys model")
		}
		m.keys = keysModel
		cmd = newCmd

		if m.keys.Exit {
			m.keys = keys.NewModel(m.styles, m.cfg.Logger, m.dbpool, m.user)
			m.status = statusReady
		} else if m.keys.Quit {
			m.status = statusQuitting
			return m, tea.Quit
		}
	case statusBrowsingTokens:
		newModel, newCmd := m.tokens.Update(msg)
		tokensModel, ok := newModel.(tokens.Model)
		if !ok {
			panic("could not perform assertion on posts model")
		}
		m.tokens = tokensModel
		cmd = newCmd

		if m.tokens.Exit {
			m.tokens = tokens.NewModel(m.styles, m.dbpool, m.user)
			m.status = statusReady
		} else if m.tokens.Quit {
			m.status = statusQuitting
			return m, tea.Quit
		}
	case statusNoAccount:
		m.createAccount, cmd = account.Update(msg, m.createAccount)
		if m.createAccount.Done {
			m.createAccount = account.NewCreateModel(m.styles, m.dbpool, m.publicKey) // reset the state
			m.status = statusReady
		} else if m.createAccount.Quit {
			m.status = statusQuitting
			return m, tea.Quit
		}
	}

	// Handle the menu
	switch m.menuChoice {
	case keysChoice:
		m.status = statusBrowsingKeys
		m.menuChoice = unsetChoice
		cmd = keys.LoadKeys(m.keys)
	case tokensChoice:
		m.status = statusBrowsingTokens
		m.menuChoice = unsetChoice
		cmd = tokens.LoadKeys(m.tokens)
	case exitChoice:
		m.status = statusQuitting
		m.dbpool.Close()
		cmd = tea.Quit
	}

	return m, cmd
}

func (m model) menuView() string {
	var s string
	for i := 0; i < len(menuChoices); i++ {
		e := "  "
		menuItem := menuChoices[menuChoice(i)]
		if i == m.menuIndex {
			e = m.styles.SelectionMarker.String() +
				m.styles.SelectedMenuItem.Render(menuItem)
		} else {
			e += menuItem
		}
		if i < len(menuChoices)-1 {
			e += "\n"
		}
		s += e
	}

	return s
}

func footerView(m model) string {
	if m.err != nil {
		return m.errorView(m.err)
	}
	return "\n\n" + common.HelpView(m.styles, "j/k, ↑/↓: choose", "enter: select")
}

func (m model) errorView(err error) string {
	head := m.styles.Error.Render("Error: ")
	body := m.styles.Subtle.Render(err.Error())
	msg := m.styles.Wrap.Render(head + body)
	return "\n\n" + indent.String(msg, 2)
}

func (m model) View() string {
	w := m.terminalSize.Width - m.styles.App.GetHorizontalFrameSize()
	s := m.styles.Logo.SetString("pico.sh").String() + "\n\n"
	switch m.status {
	case statusNoAccount:
		s += account.View(m.createAccount)
	case statusReady:
		s += m.info.View()
		s += "\n\n" + m.menuView()
		s += footerView(m)
	case statusBrowsingKeys:
		s += m.keys.View()
	case statusBrowsingTokens:
		s += m.tokens.View()
	}
	return m.styles.App.Render(wrap.String(wordwrap.String(s, w), w))
}
