package tokens

import (
	pager "github.com/charmbracelet/bubbles/paginator"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/pages"
)

const keysPerPage = 4

type state int

const (
	stateLoading state = iota
	stateNormal
	stateDeletingKey
	stateQuitting
)

type keyState int

const (
	keyNormal keyState = iota
	keySelected
	keyDeleting
)

type errMsg struct {
	err error
}

func (e errMsg) Error() string { return e.err.Error() }

type (
	keysLoadedMsg  []*db.Token
	unlinkedKeyMsg int
)

// Model is the Tea state model for this user interface.
type Model struct {
	shared common.SharedModel

	state          state
	err            error
	activeKeyIndex int         // index of the key in the below slice which is currently in use
	tokens         []*db.Token // keys linked to user's account
	index          int         // index of selected key in relation to the current page

	pager pager.Model
}

// getSelectedIndex returns the index of the cursor in relation to the total
// number of items.
func (m *Model) getSelectedIndex() int {
	return m.index + m.pager.Page*m.pager.PerPage
}

// UpdatePaging runs an update against the underlying pagination model as well
// as performing some related tasks on this model.
func (m *Model) UpdatePaging(msg tea.Msg) {
	// Handle paging
	m.pager.SetTotalPages(len(m.tokens))
	m.pager, _ = m.pager.Update(msg)

	// If selected item is out of bounds, put it in bounds
	numItems := m.pager.ItemsOnPage(len(m.tokens))
	m.index = min(m.index, numItems-1)
}

// NewModel creates a new model with defaults.
func NewModel(shared common.SharedModel) Model {
	p := pager.New()
	p.PerPage = keysPerPage
	p.Type = pager.Dots
	p.InactiveDot = shared.Styles.InactivePagination.Render("•")

	return Model{
		shared: shared,

		state:          stateLoading,
		err:            nil,
		activeKeyIndex: -1,
		tokens:         []*db.Token{},
		index:          0,

		pager: p,
	}
}

// Init is the Tea initialization function.
func (m Model) Init() tea.Cmd {
	return FetchTokens(m.shared)
}

// Update is the tea update function which handles incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m, pages.Navigate(pages.MenuPage)
		case "up", "k":
			m.index--
			if m.index < 0 && m.pager.Page > 0 {
				m.index = m.pager.PerPage - 1
				m.pager.PrevPage()
			}
			m.index = max(0, m.index)
		case "down", "j":
			itemsOnPage := m.pager.ItemsOnPage(len(m.tokens))
			m.index++
			if m.index > itemsOnPage-1 && m.pager.Page < m.pager.TotalPages-1 {
				m.index = 0
				m.pager.NextPage()
			}
			m.index = min(itemsOnPage-1, m.index)

		case "n":
			return m, pages.Navigate(pages.CreateTokenPage)

		// Delete
		case "x":
			m.state = stateDeletingKey
			m.UpdatePaging(msg)
			return m, nil

		// Confirm Delete
		case "y":
			switch m.state {
			case stateDeletingKey:
				m.state = stateNormal
				return m, m.unlinkKey()
			}
		}

	case errMsg:
		m.err = msg.err
		return m, nil

	case keysLoadedMsg:
		m.state = stateNormal
		m.index = 0
		m.tokens = msg

	case unlinkedKeyMsg:
		if m.state == stateQuitting {
			return m, tea.Quit
		}
		i := m.getSelectedIndex()

		// Remove key from array
		m.tokens = append(m.tokens[:i], m.tokens[i+1:]...)

		// Update pagination
		m.pager.SetTotalPages(len(m.tokens))
		m.pager.Page = min(m.pager.Page, m.pager.TotalPages-1)

		// Update cursor
		m.index = min(m.index, m.pager.ItemsOnPage(len(m.tokens)-1))

		return m, nil

	// leaving page so reset model
	case pages.NavigateMsg:
		next := NewModel(m.shared)
		return next, next.Init()
	}

	switch m.state {
	case stateDeletingKey:
		// If an item is being confirmed for delete, any key (other than the key
		// used for confirmation above) cancels the deletion
		k, ok := msg.(tea.KeyMsg)
		if ok && k.String() != "y" {
			m.state = stateNormal
		}
	}

	m.UpdatePaging(msg)
	return m, tea.Batch(cmds...)
}

// View renders the current UI into a string.
func (m Model) View() string {
	if m.err != nil {
		return m.err.Error()
	}

	var s string

	switch m.state {
	case stateLoading:
		s = "Loading...\n\n"
	case stateQuitting:
		s = "Thanks for using pico.sh!\n"
	default:
		s = "Here are the tokens linked to your account.\n\n"
		s += "A token can be used for connecting to our\nIRC bouncer from your client.\n\n"

		// Keys
		s += keysView(m)
		if m.pager.TotalPages > 1 {
			s += m.pager.View()
		}

		// Footer
		switch m.state {
		case stateDeletingKey:
			s += m.promptView("Delete this key?")
		default:
			s += "\n\n" + m.helpView()
		}
	}

	return s
}

func keysView(m Model) string {
	var (
		s          string
		state      keyState
		start, end = m.pager.GetSliceBounds(len(m.tokens))
		slice      = m.tokens[start:end]
	)

	destructiveState := m.state == stateDeletingKey

	// Render key info
	for i, key := range slice {
		if destructiveState && m.index == i {
			state = keyDeleting
		} else if m.index == i {
			state = keySelected
		} else {
			state = keyNormal
		}
		s += newStyledKey(m.shared.Styles, key, i+start == m.activeKeyIndex).render(state)
	}

	// If there aren't enough keys to fill the view, fill the missing parts
	// with whitespace
	if len(slice) < m.pager.PerPage {
		for i := len(slice); i < m.pager.PerPage; i++ {
			s += "\n\n\n"
		}
	}

	return s
}

func (m *Model) helpView() string {
	var items []string
	if len(m.tokens) > 1 {
		items = append(items, "j/k, ↑/↓: choose")
	}
	if m.pager.TotalPages > 1 {
		items = append(items, "h/l, ←/→: page")
	}
	items = append(items, []string{"x: delete", "n: create", "esc: exit"}...)
	return common.HelpView(m.shared.Styles, items...)
}

func (m *Model) promptView(prompt string) string {
	st := m.shared.Styles.Delete.Copy().MarginTop(2).MarginRight(1)
	return st.Render(prompt) +
		m.shared.Styles.Delete.Render("(y/N)")
}

func FetchTokens(shrd common.SharedModel) tea.Cmd {
	return func() tea.Msg {
		ak, err := shrd.Dbpool.FindTokensForUser(shrd.User.ID)
		if err != nil {
			return errMsg{err}
		}
		return keysLoadedMsg(ak)
	}
}

// unlinkKey deletes the selected key.
func (m Model) unlinkKey() tea.Cmd {
	return func() tea.Msg {
		id := m.tokens[m.getSelectedIndex()].ID
		err := m.shared.Dbpool.RemoveToken(id)
		if err != nil {
			return errMsg{err}
		}
		return unlinkedKeyMsg(m.index)
	}
}

// Utils

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
