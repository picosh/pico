package menu

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/reflow/indent"
	"github.com/picosh/pico/tui/common"
)

// menuChoice represents a chosen menu item.
type menuChoice int

type MenuChoiceMsg struct {
	MenuChoice menuChoice
}

const (
	KeysChoice menuChoice = iota
	TokensChoice
	NotificationsChoice
	PlusChoice
	SettingsChoice
	LogsChoice
	AnalyticsChoice
	ChatChoice
	ExitChoice
	UnsetChoice // set when no choice has been made
)

// menu text corresponding to menu choices. these are presented to the user.
var menuChoices = map[menuChoice]string{
	KeysChoice:          "Manage pubkeys",
	TokensChoice:        "Manage tokens",
	NotificationsChoice: "Notifications",
	PlusChoice:          "Pico+",
	SettingsChoice:      "Settings",
	LogsChoice:          "Logs",
	AnalyticsChoice:     "Analytics",
	ChatChoice:          "Chat",
	ExitChoice:          "Exit",
}

type Model struct {
	shared     *common.SharedModel
	err        error
	menuIndex  int
	menuChoice menuChoice
}

func NewModel(shared *common.SharedModel) Model {
	return Model{
		shared:     shared,
		menuChoice: UnsetChoice,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.shared.Dbpool.Close()
			return m, tea.Quit
		}

		switch msg.String() {
		case "q", "esc":
			m.shared.Dbpool.Close()
			return m, tea.Quit

		case "up", "k":
			m.menuIndex--
			if m.menuIndex < 0 {
				m.menuIndex = len(menuChoices) - 1
			}

		case "enter":
			m.menuChoice = menuChoice(m.menuIndex)
			cmds = append(cmds, MenuMsg(m.menuChoice))

		case "down", "j":
			m.menuIndex++
			if m.menuIndex >= len(menuChoices) {
				m.menuIndex = 0
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func MenuMsg(choice menuChoice) tea.Cmd {
	return func() tea.Msg {
		return MenuChoiceMsg{
			MenuChoice: choice,
		}
	}
}

func (m Model) bioView() string {
	if m.shared.User == nil {
		return "Loading user info..."
	}

	var username string
	if m.shared.User.Name != "" {
		username = m.shared.User.Name
	} else {
		username = m.shared.Styles.Subtle.Render("(none set)")
	}

	expires := ""
	if m.shared.PlusFeatureFlag != nil {
		expires = m.shared.PlusFeatureFlag.ExpiresAt.Format(common.DateFormat)
	}

	vals := []string{
		"Username", username,
		"Joined", m.shared.User.CreatedAt.Format(common.DateFormat),
	}

	if expires != "" {
		vals = append(vals, "Pico+ Expires", expires)
	}

	return common.KeyValueView(m.shared.Styles, vals...)
}

func (m Model) menuView() string {
	var s string
	for i := 0; i < len(menuChoices); i++ {
		e := "  "
		menuItem := menuChoices[menuChoice(i)]
		if i == m.menuIndex {
			e = m.shared.Styles.SelectionMarker.String() +
				m.shared.Styles.SelectedMenuItem.Render(menuItem)
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

func (m Model) errorView(err error) string {
	head := m.shared.Styles.Error.Render("Error: ")
	body := m.shared.Styles.Subtle.Render(err.Error())
	msg := m.shared.Styles.Wrap.Render(head + body)
	return "\n\n" + indent.String(msg, 2)
}

func (m Model) footerView() string {
	if m.err != nil {
		return m.errorView(m.err)
	}
	return "\n\n" + common.HelpView(m.shared.Styles, "j/k, ↑/↓: choose", "enter: select")
}

func (m Model) View() string {
	s := m.bioView()
	s += "\n\n" + m.menuView()
	s += m.footerView()
	return s
}
