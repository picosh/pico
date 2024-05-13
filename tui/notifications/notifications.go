package notifications

import (
	"fmt"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
)

func NotificationsView(dbpool db.DB, userID string) string {
	pass, err := dbpool.UpsertToken(userID, "pico-rss")
	if err != nil {
		return err.Error()
	}
	url := fmt.Sprintf("https://auth.pico.sh/rss/%s", pass)
	md := fmt.Sprintf(`# Notifications

We provide a special RSS feed for all pico users where we can send
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
	Done bool // true when it's time to exit this view
	Quit bool // true when the user wants to quit the whole program

	styles   common.Styles
	user     *db.User
	viewport viewport.Model
}

func headerHeight(styles common.Styles) int {
	return 0
}

func headerWidth(w int) int {
	return w - 2
}

// NewModel returns a new username model in its initial state.
func NewModel(styles common.Styles, dbpool db.DB, user *db.User, session ssh.Session) Model {
	pty, _, _ := session.Pty()
	hh := headerHeight(styles)
	viewport := viewport.New(headerWidth(pty.Window.Width), pty.Window.Height-hh)
	viewport.YPosition = hh
	viewport.SetContent(NotificationsView(dbpool, user.ID))

	return Model{
		Done:     false,
		Quit:     false,
		styles:   styles,
		user:     user,
		viewport: viewport,
	}
}

// Update is the Bubble Tea update loop.
func Update(msg tea.Msg, m Model) (Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.Quit = true
		case tea.KeyEscape:
			m.Done = true

		default:
			switch msg.String() {
			case "q":
				m.Done = true
			}
		}

	case tea.WindowSizeMsg:
		m.viewport.Width = headerWidth(msg.Width)
		hh := headerHeight(m.styles)
		m.viewport.Height = msg.Height - hh
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// View renders current view from the model.
func View(m Model) string {
	s := m.viewport.View()
	return s
}
