package chat

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/pages"
)

var maxWidth = 55

type focus int

const (
	focusNone = iota
	focusChat
)

type Model struct {
	shared *common.SharedModel
	focus  focus
}

func NewModel(shrd *common.SharedModel) Model {
	return Model{
		shared: shrd,
		focus:  focusChat,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m, pages.Navigate(pages.MenuPage)
		case "tab":
			if m.focus == focusNone {
				m.focus = focusChat
			} else {
				m.focus = focusNone
			}
		case "enter":
			if m.focus == focusChat {
				return m, m.gotoChat()
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	return m.analyticsView()
}

func (m Model) analyticsView() string {
	banner := `We provide a managed IRC bouncer for pico+ users.  When you click the button we will open our TUI chat with your user authenticated automatically.

If you haven't configured your pico+ account with our IRC bouncer, the guide is here:

	https://pico.sh/bouncer

If you want to quickly chat with us on IRC without pico+, go to the web chat:

	https://web.libera.chat/gamja?autojoin=#pico.sh`

	str := ""
	hasPlus := m.shared.PlusFeatureFlag != nil
	if hasPlus {
		hasFocus := m.focus == focusChat
		str += banner + "\n\nLet's Chat!  " + common.OKButtonView(m.shared.Styles, hasFocus, true)
	} else {
		str += banner + "\n\n" + m.shared.Styles.Error.SetString("Our IRC Bouncer is only available to pico+ users.").String()
	}

	return m.shared.Styles.RoundedBorder.Width(maxWidth).SetString(str).String()
}

func (m Model) gotoChat() tea.Cmd {
	return LoadChat(m.shared)
}
