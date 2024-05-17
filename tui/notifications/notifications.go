package notifications

import (
	"fmt"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/pages"
)

func NotificationsView(dbpool db.DB, userID string) string {
	pass, err := dbpool.UpsertToken(userID, "pico-rss")
	if err != nil {
		return err.Error()
	}
	url := fmt.Sprintf("https://auth.pico.sh/rss/%s", pass)
	md := fmt.Sprintf(`We provide a special RSS feed for all pico users where we can send
user-specific notifications. This is where we will send pico+
expiration notices, among other alerts. To be clear, this is
optional but **highly** recommended.

Add this URL to your RSS feed reader:

%s

## Using our [rss-to-email](https://pico.sh/feeds) service

Create a feeds file (e.g. pico.txt):`, url)

	md += "\n```\n"
	md += fmt.Sprintf(`=: email rss@myemail.com
=: digest_interval 1day
=> %s
`, url)
	md += "\n```\n"
	md += "Then copy the file to us:\n"
	md += "```\nrsync pico.txt feeds.pico.sh:/\n```"

	r, _ := glamour.NewTermRenderer(
		// detect background color and pick either the default dark or light theme
		glamour.WithAutoStyle(),
	)
	out, err := r.Render(md)
	if err != nil {
		return err.Error()
	}
	return out
}

// Model holds the state of the username UI.
type Model struct {
	shared common.SharedModel

	Done bool // true when it's time to exit this view
	Quit bool // true when the user wants to quit the whole program

	viewport viewport.Model
}

func headerHeight(shrd common.SharedModel) int {
	return shrd.HeaderHeight
}

func headerWidth(w int) int {
	return w - 2
}

func NewModel(shared common.SharedModel) Model {
	hh := headerHeight(shared)
	viewport := viewport.New(headerWidth(shared.Width), shared.Height-hh)
	viewport.YPosition = hh
	if shared.User != nil {
		viewport.SetContent(NotificationsView(shared.Dbpool, shared.User.ID))
	}

	return Model{
		shared: shared,

		Done:     false,
		Quit:     false,
		viewport: viewport,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.viewport.Width = headerWidth(m.shared.Width)
		hh := headerHeight(m.shared)
		m.viewport.Height = m.shared.Height - hh

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m, pages.Navigate(pages.MenuPage)
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	return m.viewport.View()
}
