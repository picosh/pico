package pages

import tea "github.com/charmbracelet/bubbletea"

type Page int

const (
	MenuPage Page = iota
	CreateAccountPage
	CreatePubkeyPage
	CreateTokenPage
	PubkeysPage
	TokensPage
	NotificationsPage
	PlusPage
)

type NavigateMsg struct{ Page }

func Navigate(page Page) tea.Cmd {
	return func() tea.Msg {
		return NavigateMsg{page}
	}
}
