package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/wish"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/reflow/wrap"
	"github.com/picosh/pico/tui/analytics"
	"github.com/picosh/pico/tui/chat"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/createaccount"
	"github.com/picosh/pico/tui/createkey"
	"github.com/picosh/pico/tui/createtoken"
	"github.com/picosh/pico/tui/logs"
	"github.com/picosh/pico/tui/menu"
	"github.com/picosh/pico/tui/notifications"
	"github.com/picosh/pico/tui/pages"
	"github.com/picosh/pico/tui/plus"
	"github.com/picosh/pico/tui/pubkeys"
	"github.com/picosh/pico/tui/settings"
	"github.com/picosh/pico/tui/tokens"
)

type state int

const (
	initState state = iota
	readyState
)

// Just a generic tea.Model to demo terminal information of ssh.
type UI struct {
	shared *common.SharedModel

	state      state
	activePage pages.Page
	pages      []tea.Model
}

func NewUI(shared *common.SharedModel) *UI {
	m := &UI{
		shared: shared,
		state:  initState,
		pages:  make([]tea.Model, 12),
	}
	return m
}

func (m *UI) updateActivePage(msg tea.Msg) tea.Cmd {
	nm, cmd := m.pages[m.activePage].Update(msg)
	m.pages[m.activePage] = nm
	return cmd
}

func (m *UI) setupUser() error {
	user, err := findUser(m.shared)
	if err != nil {
		m.shared.Logger.Error("cannot find user", "err", err)
		wish.Errorf(m.shared.Session, "\nERROR: %s\n\n", err)
		return err
	}

	m.shared.User = user
	ff, _ := findPlusFeatureFlag(m.shared)
	m.shared.PlusFeatureFlag = ff

	return nil
}

func (m *UI) Init() tea.Cmd {
	// header height is required to calculate viewport for
	// some pages
	m.shared.HeaderHeight = lipgloss.Height(m.header()) + 1

	m.pages[pages.MenuPage] = menu.NewModel(m.shared)
	m.pages[pages.CreateAccountPage] = createaccount.NewModel(m.shared)
	m.pages[pages.CreatePubkeyPage] = createkey.NewModel(m.shared)
	m.pages[pages.CreateTokenPage] = createtoken.NewModel(m.shared)
	m.pages[pages.CreateAccountPage] = createaccount.NewModel(m.shared)
	m.pages[pages.PubkeysPage] = pubkeys.NewModel(m.shared)
	m.pages[pages.TokensPage] = tokens.NewModel(m.shared)
	m.pages[pages.NotificationsPage] = notifications.NewModel(m.shared)
	m.pages[pages.PlusPage] = plus.NewModel(m.shared)
	m.pages[pages.SettingsPage] = settings.NewModel(m.shared)
	m.pages[pages.LogsPage] = logs.NewModel(m.shared)
	m.pages[pages.AnalyticsPage] = analytics.NewModel(m.shared)
	m.pages[pages.ChatPage] = chat.NewModel(m.shared)
	if m.shared.User == nil {
		m.activePage = pages.CreateAccountPage
	} else {
		m.activePage = pages.MenuPage
	}
	m.state = readyState
	return nil
}

func (m *UI) updateModels(msg tea.Msg) tea.Cmd {
	cmds := []tea.Cmd{}
	for i, page := range m.pages {
		if page == nil {
			continue
		}
		nm, cmd := page.Update(msg)
		m.pages[i] = nm
		cmds = append(cmds, cmd)
	}
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

	case pages.NavigateMsg:
		// send message to the active page so it can teardown
		// and reset itself
		cmds = append(cmds, m.updateActivePage(msg))
		m.activePage = msg.Page

	// user created account
	case createaccount.CreateAccountMsg:
		_ = m.setupUser()
		// reset model and pages
		return m, m.Init()

	case menu.MenuChoiceMsg:
		switch msg.MenuChoice {
		case menu.KeysChoice:
			m.activePage = pages.PubkeysPage
		case menu.TokensChoice:
			m.activePage = pages.TokensPage
		case menu.NotificationsChoice:
			m.activePage = pages.NotificationsPage
		case menu.PlusChoice:
			m.activePage = pages.PlusPage
		case menu.SettingsChoice:
			m.activePage = pages.SettingsPage
		case menu.LogsChoice:
			m.activePage = pages.LogsPage
		case menu.AnalyticsChoice:
			m.activePage = pages.AnalyticsPage
		case menu.ChatChoice:
			m.activePage = pages.ChatPage
		case menu.ExitChoice:
			m.shared.Dbpool.Close()
			return m, tea.Quit
		}

		cmds = append(cmds, m.pages[m.activePage].Init())
	}

	cmd := m.updateActivePage(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *UI) header() string {
	logoTxt := "pico.sh"
	ff := m.shared.PlusFeatureFlag
	if ff != nil && ff.IsValid() {
		logoTxt = "pico+"
	}

	logo := m.shared.
		Styles.
		Logo.
		SetString(logoTxt)
	title := m.shared.
		Styles.
		Note.
		SetString(pages.ToTitle(m.activePage))
	div := m.shared.
		Styles.
		HelpDivider.
		Foreground(common.Green)
	s := fmt.Sprintf("%s%s%s\n\n", logo, div, title)
	return s
}

func (m *UI) View() string {
	s := m.header()

	if m.pages[m.activePage] != nil {
		s += m.pages[m.activePage].View()
	}

	width := m.shared.Width - m.shared.Styles.App.GetHorizontalFrameSize()
	maxWidth := width
	str := wrap.String(
		wordwrap.String(s, maxWidth),
		maxWidth,
	)
	return m.shared.Styles.App.Render(str)
}
