package pubkeys

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
	stateDeletingActiveKey
	stateDeletingAccount
	stateQuitting
)

type keyState int

const (
	keyNormal keyState = iota
	keySelected
	keyDeleting
)

type (
	keysLoadedMsg  []*db.PublicKey
	unlinkedKeyMsg int
)

// Model is the Tea state model for this user interface.
type Model struct {
	shared common.SharedModel

	state          state
	err            error
	activeKeyIndex int             // index of the key in the below slice which is currently in use
	keys           []*db.PublicKey // keys linked to user's account
	index          int             // index of selected key in relation to the current page

	pager pager.Model
}

// NewModel creates a new model with defaults.
func NewModel(shared common.SharedModel) Model {
	p := pager.New()
	p.PerPage = keysPerPage
	p.Type = pager.Dots
	p.InactiveDot = shared.Styles.InactivePagination.Render("•")

	return Model{
		shared: shared,

		pager:          p,
		state:          stateLoading,
		err:            nil,
		activeKeyIndex: -1,
		keys:           []*db.PublicKey{},
		index:          0,
	}
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
	m.pager.SetTotalPages(len(m.keys))
	m.pager, _ = m.pager.Update(msg)

	// If selected item is out of bounds, put it in bounds
	numItems := m.pager.ItemsOnPage(len(m.keys))
	m.index = min(m.index, numItems-1)
}

// Init is the Tea initialization function.
func (m Model) Init() tea.Cmd {
	return FetchKeys(m.shared)
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
			m.index -= 1
			if m.index < 0 && m.pager.Page > 0 {
				m.index = m.pager.PerPage - 1
				m.pager.PrevPage()
			}
			m.index = max(0, m.index)
		case "down", "j":
			itemsOnPage := m.pager.ItemsOnPage(len(m.keys))
			m.index += 1
			if m.index > itemsOnPage-1 && m.pager.Page < m.pager.TotalPages-1 {
				m.index = 0
				m.pager.NextPage()
			}
			m.index = min(itemsOnPage-1, m.index)

		case "n":
			return m, pages.Navigate(pages.CreatePubkeyPage)

		// Delete
		case "x":
			m.state = stateDeletingKey
			m.UpdatePaging(msg)
			return m, nil

		// Confirm Delete
		case "y":
			switch m.state {
			case stateDeletingKey:
				if len(m.keys) == 1 {
					// The user is about to delete her account. Double confirm.
					m.state = stateDeletingAccount
					return m, nil
				}
				if m.getSelectedIndex() == m.activeKeyIndex {
					// The user is going to delete her active key. Double confirm.
					m.state = stateDeletingActiveKey
					return m, nil
				}
				m.state = stateNormal
				return m, m.unlinkKey()
			case stateDeletingActiveKey:
				m.state = stateQuitting
				// Active key will be deleted. Remove the key and exit.
				return m, m.unlinkKey()
			case stateDeletingAccount:
				// Account will be deleted. Remove the key and exit.
				m.state = stateQuitting
				return m, m.deleteAccount()
			}
		}

	case common.ErrMsg:
		m.err = msg.Err
		return m, nil

	case keysLoadedMsg:
		m.state = stateNormal
		m.index = 0
		m.keys = msg
		for i, key := range m.keys {
			if key.Key == m.shared.User.PublicKey.Key {
				m.activeKeyIndex = i
			}
		}

	case unlinkedKeyMsg:
		if m.state == stateQuitting {
			return m, tea.Quit
		}
		i := m.getSelectedIndex()

		// Remove key from array
		m.keys = append(m.keys[:i], m.keys[i+1:]...)

		// Update pagination
		m.pager.SetTotalPages(len(m.keys))
		m.pager.Page = min(m.pager.Page, m.pager.TotalPages-1)

		// Update cursor
		m.index = min(m.index, m.pager.ItemsOnPage(len(m.keys)-1))
		for i, key := range m.keys {
			if key.Key == m.shared.User.PublicKey.Key {
				m.activeKeyIndex = i
			}
		}

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
		s = "Here are the pubkeys linked to your account. Add more pubkeys to be able to login with multiple SSH keypairs.\n\n"

		// Keys
		s += m.keysView()
		if m.pager.TotalPages > 1 {
			s += m.pager.View()
		}

		// Footer
		switch m.state {
		case stateDeletingKey:
			s += m.promptView("Delete this key?")
		case stateDeletingActiveKey:
			s += m.promptView("This is the key currently in use. Are you, like, for-sure-for-sure?")
		case stateDeletingAccount:
			s += m.promptView("Sure? This will delete your account. Are you absolutely positive?")
		default:
			s += "\n\n" + m.helpView()
		}
	}

	return s
}

func (m *Model) keysView() string {
	var (
		s          string
		state      keyState
		start, end = m.pager.GetSliceBounds(len(m.keys))
		slice      = m.keys[start:end]
	)

	destructiveState :=
		(m.state == stateDeletingKey ||
			m.state == stateDeletingActiveKey ||
			m.state == stateDeletingAccount)

	// Render key info
	for i, key := range slice {
		if destructiveState && m.index == i {
			state = keyDeleting
		} else if m.index == i {
			state = keySelected
		} else {
			state = keyNormal
		}
		s += m.newStyledKey(m.shared.Styles, key, i+start == m.activeKeyIndex).render(state)
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
	if len(m.keys) > 1 {
		items = append(items, "j/k, ↑/↓: choose")
	}
	if m.pager.TotalPages > 1 {
		items = append(items, "h/l, ←/→: page")
	}
	items = append(items, []string{"x: delete", "n: create", "esc: exit"}...)
	return common.HelpView(m.shared.Styles, items...)
}

func (m *Model) promptView(prompt string) string {
	st := m.shared.Styles.Delete.MarginTop(2).MarginRight(1)
	return st.Render(prompt) +
		m.shared.Styles.Delete.Render("(y/N)")
}

// FetchKeys loads the current set of keys via the charm client.
func FetchKeys(shrd common.SharedModel) tea.Cmd {
	return func() tea.Msg {
		ak, err := shrd.Dbpool.FindKeysForUser(shrd.User)
		if err != nil {
			return common.ErrMsg{Err: err}
		}
		return keysLoadedMsg(ak)
	}
}

// unlinkKey deletes the selected key.
func (m *Model) unlinkKey() tea.Cmd {
	return func() tea.Msg {
		id := m.keys[m.getSelectedIndex()].ID
		err := m.shared.Dbpool.RemoveKeys([]string{id})
		if err != nil {
			return common.ErrMsg{Err: err}
		}
		return unlinkedKeyMsg(m.index)
	}
}

func (m *Model) deleteAccount() tea.Cmd {
	return func() tea.Msg {
		id := m.keys[m.getSelectedIndex()].UserID
		m.shared.Logger.Info("user requested account deletion", "user", m.shared.User.Name, "id", id)
		err := m.shared.Dbpool.RemoveUsers([]string{id})
		if err != nil {
			return common.ErrMsg{Err: err}
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
