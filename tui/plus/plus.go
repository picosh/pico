package plus

import (
	"fmt"
	"net/url"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/pages"
)

func PlusView(username string, w int) string {
	paymentLink := "https://auth.pico.sh/checkout"
	url := fmt.Sprintf("%s/%s", paymentLink, url.QueryEscape(username))
	md := fmt.Sprintf(`Signup to get premium access

## $2/month (billed annually)

- tuns
  - full access
- pages
  - full access
  - per-site analytics
- prose
  - increased storage limits
  - blog analytics
- 10GB total storage

There are a few ways to purchase a membership. We try our best to
provide immediate access to pico+ regardless of payment
method.

## Online Payment (credit card, paypal)

%s

## Snail Mail

Send cash (USD) or check to:
- pico.sh LLC
- 206 E Huron St STE 103
- Ann Arbor MI 48104

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
		glamour.WithWordWrap(w-20),
	)
	out, _ := r.Render(md)
	return out
}

// Model holds the state of the username UI.
type Model struct {
	shared   common.SharedModel
	viewport viewport.Model
}

func headerHeight(shrd common.SharedModel) int {
	return shrd.HeaderHeight
}

func headerWidth(w int) int {
	return w - 2
}

// NewModel returns a new username model in its initial state.
func NewModel(shared common.SharedModel) Model {
	hh := headerHeight(shared)
	ww := headerWidth(shared.Width)
	viewport := viewport.New(ww, shared.Height-hh)
	if shared.User != nil {
		viewport.SetContent(PlusView(shared.User.Name, ww))
	}

	return Model{
		shared:   shared,
		viewport: viewport,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

// Update is the Bubble Tea update loop.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.viewport.Width = headerWidth(msg.Width)
		hh := headerHeight(m.shared)
		m.viewport.Height = msg.Height - hh
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

// View renders current view from the model.
func (m Model) View() string {
	return m.viewport.View()
}
