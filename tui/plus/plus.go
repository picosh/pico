package plus

import (
	"fmt"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
)

func PlusView(username string) string {
	paymentLink := "https://checkout.pico.sh/buy/73c26cf9-3fac-44c3-b744-298b3032a96b?discount=0"
	url := fmt.Sprintf("%s&checkout[custom][username]=%s", paymentLink, username)
	md := fmt.Sprintf(`# pico+

Signup to get premium access

## $2/month (billed annually)

- tuns
  - full access
- pages
  - full access
  - per-site analytics
- prose
  - increased storage limits
  - blog analytics
- docker registry
  - full access

There are a few ways to purchase a membership. We try our best to
provide immediate access to <code>pico+</code> regardless of payment
method.

## Online Payment (credit card, paypal)

%s

## Snail Mail

Send cash (USD) or check to:
- pico.sh LLC
- 206 E Huron St STE 103
- Ann Arbor MI 48104

## What are the storage limits?

We don't want pico+ users to think about storage limits.  For all
intents and purposes, there are no storage restrictions.  Having said
that, if we detect abuse or feel like a user is storing too much, we
will notify the user and potentially suspend their account.

Again, most users do not need to worry.

## Notes

Have any questions not covered here? [Email](mailto:hello@pico.sh)
us or join [IRC](https://pico.sh/irc), we will promptly respond.

We do not maintain active subscriptions for pico+.
Every year you must pay again. We do not take monthly payments, you
must pay for a year up-front. Pricing is subject to change because
we plan on continuing to include more services as we build them.`, url)

	r, _ := glamour.NewTermRenderer(
		// detect background color and pick either the default dark or light theme
		glamour.WithAutoStyle(),
	)
	out, _ := r.Render(md)
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
func NewModel(styles common.Styles, user *db.User, session ssh.Session) Model {
	pty, _, _ := session.Pty()
	hh := headerHeight(styles)
	viewport := viewport.New(headerWidth(pty.Window.Width), pty.Window.Height-hh)
	viewport.YPosition = hh
	if user != nil {
		viewport.SetContent(PlusView(user.Name))
	}

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
