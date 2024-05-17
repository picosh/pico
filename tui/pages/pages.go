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
	SettingsPage
)

type NavigateMsg struct{ Page }

func Navigate(page Page) tea.Cmd {
	return func() tea.Msg {
		return NavigateMsg{page}
	}
}

func ToTitle(page Page) string {
	switch page {
	case CreateAccountPage:
		return "create account"
	case CreatePubkeyPage:
		return "add pubkey"
	case CreateTokenPage:
		return "new api token"
	case MenuPage:
		return "menu"
	case NotificationsPage:
		return "notifications"
	case PlusPage:
		return "pico+"
	case TokensPage:
		return "api tokens"
	case PubkeysPage:
		return "pubkeys"
	case SettingsPage:
		return "settings"
	}

	return ""
}
