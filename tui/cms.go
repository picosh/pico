package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/reflow/wrap"
	"github.com/picosh/pico/tui/account"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/keys"
	"github.com/picosh/pico/tui/menu"
	"github.com/picosh/pico/tui/notifications"
	"github.com/picosh/pico/tui/plus"
	"github.com/picosh/pico/tui/tokens"
)

type page int

const (
	menuPage page = iota
	createAccountPage
	pubkeysPage
	tokensPage
	notificationsPage
	plusPage
)

type state int

const (
	initState state = iota
	readyState
)

// Just a generic tea.Model to demo terminal information of ssh.
type UI struct {
	shared common.SharedModel

	state      state
	activePage page
	pages      []tea.Model
}

func NewUI(shared common.SharedModel) *UI {
	m := &UI{
		shared: shared,
		state:  initState,
		pages:  make([]tea.Model, 6),
	}
	return m
}

func (m *UI) Init() tea.Cmd {
	m.pages[menuPage] = menu.NewModel(m.shared)
	m.pages[createAccountPage] = account.NewModel(m.shared)
	m.pages[pubkeysPage] = keys.NewModel(m.shared)
	m.pages[tokensPage] = tokens.NewModel(m.shared)
	m.pages[notificationsPage] = notifications.NewModel(m.shared)
	m.pages[plusPage] = plus.NewModel(m.shared)
	if m.shared.User == nil {
		m.activePage = createAccountPage
	} else {
		m.activePage = menuPage
	}
	m.state = readyState
	cmds := make([]tea.Cmd, 0)
	return tea.Batch(cmds...)
}

func (m *UI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.shared.Width = msg.Width
		m.shared.Height = msg.Height
		return m, m.updateModels(msg)

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.shared.Dbpool.Close()
			return m, tea.Quit
		}

	case common.ExitMsg:
		// send message to the active page so it can teardown
		// and reset itself
		cmds = append(cmds, m.updateActivePage(msg))
		m.activePage = menuPage

	case account.CreateAccountMsg:
		m.state = readyState
		m.shared.User = msg
		cmds = append(cmds, m.updateModels(msg))

	case menu.MenuChoiceMsg:
		switch msg.MenuChoice {
		case menu.KeysChoice:
			m.activePage = pubkeysPage
			cmds = append(cmds, keys.FetchKeys(m.shared))
		case menu.TokensChoice:
			m.activePage = tokensPage
			cmds = append(cmds, tokens.FetchTokens(m.shared))
		case menu.PlusChoice:
			m.activePage = plusPage
		case menu.NotificationsChoice:
			m.activePage = notificationsPage
		case menu.ChatChoice:
			cmds = append(cmds, LoadChat(m.shared))
		case menu.ExitChoice:
			m.shared.Dbpool.Close()
			return m, tea.Quit
		}
	}

	cmd := m.updateActivePage(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *UI) updateActivePage(msg tea.Msg) tea.Cmd {
	nm, cmd := m.pages[m.activePage].Update(msg)
	m.pages[m.activePage] = nm
	return cmd
}

func (m *UI) updateModels(msg tea.Msg) tea.Cmd {
	cmds := []tea.Cmd{}
	for i, page := range m.pages {
		nm, cmd := page.Update(msg)
		m.pages[i] = nm
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (m *UI) View() string {
	w := m.shared.Width - m.shared.Styles.App.GetHorizontalFrameSize()
	s := m.shared.Styles.Logo.SetString("pico.sh").String() + "\n\n"
	s += m.pages[m.activePage].View()
	str := wrap.String(wordwrap.String(s, w), w)
	return m.shared.Styles.App.Render(str)
}
