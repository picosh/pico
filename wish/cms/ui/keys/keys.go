package keys

import (
	"fmt"

	"git.sr.ht/~erock/pico/wish/cms/config"
	"git.sr.ht/~erock/pico/wish/cms/db"
	"git.sr.ht/~erock/pico/wish/cms/ui/common"
	"git.sr.ht/~erock/pico/wish/cms/ui/createkey"
	pager "github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

const keysPerPage = 4

type state int

const (
	stateLoading state = iota
	stateNormal
	stateDeletingKey
	stateDeletingActiveKey
	stateDeletingAccount
	stateCreateKey
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
	keysLoadedMsg  []*db.PublicKey
	unlinkedKeyMsg int
)

// Model is the Tea state model for this user interface.
type Model struct {
	cfg            *config.ConfigCms
	dbpool         db.DB
	user           *db.User
	styles         common.Styles
	pager          pager.Model
	state          state
	err            error
	activeKeyIndex int             // index of the key in the below slice which is currently in use
	keys           []*db.PublicKey // keys linked to user's account
	index          int             // index of selected key in relation to the current page
	Exit           bool
	Quit           bool
	spinner        spinner.Model
	createKey      createkey.Model
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

// NewModel creates a new model with defaults.
func NewModel(cfg *config.ConfigCms, dbpool db.DB, user *db.User) Model {
	st := common.DefaultStyles()

	p := pager.NewModel()
	p.PerPage = keysPerPage
	p.Type = pager.Dots
	p.InactiveDot = st.InactivePagination.Render("•")

	return Model{
		cfg:            cfg,
		dbpool:         dbpool,
		user:           user,
		styles:         st,
		pager:          p,
		state:          stateLoading,
		err:            nil,
		activeKeyIndex: -1,
		keys:           []*db.PublicKey{},
		index:          0,
		spinner:        common.NewSpinner(),
		Exit:           false,
		Quit:           false,
	}
}

// Init is the Tea initialization function.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		spinner.Tick,
	)
}

// Update is the tea update function which handles incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmds []tea.Cmd
		cmd  tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.Exit = true
			return m, nil
		}

		if m.state != stateCreateKey {
			switch msg.String() {
			case "q", "esc":
				m.Exit = true
				return m, nil
			case "up", "k":
				m.index--
				if m.index < 0 && m.pager.Page > 0 {
					m.index = m.pager.PerPage - 1
					m.pager.PrevPage()
				}
				m.index = max(0, m.index)
			case "down", "j":
				itemsOnPage := m.pager.ItemsOnPage(len(m.keys))
				m.index++
				if m.index > itemsOnPage-1 && m.pager.Page < m.pager.TotalPages-1 {
					m.index = 0
					m.pager.NextPage()
				}
				m.index = min(itemsOnPage-1, m.index)

			case "n":
				m.state = stateCreateKey
				return m, nil

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
					return m, unlinkKey(m)
				case stateDeletingActiveKey:
					// Active key will be deleted. Remove the key and exit.
					fallthrough
				case stateDeletingAccount:
					// Account will be deleted. Remove the key and exit.
					m.state = stateQuitting
					return m, deleteAccount(m)
				}
			}
		}

	case errMsg:
		m.err = msg.err
		return m, nil

	case keysLoadedMsg:
		m.state = stateNormal
		m.index = 0
		m.keys = msg
		for i, key := range m.keys {
			if key.Key == m.user.PublicKey.Key {
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
			if key.Key == m.user.PublicKey.Key {
				m.activeKeyIndex = i
			}
		}

		return m, nil

	case createkey.KeySetMsg:
		m.state = stateNormal
		return m, fetchKeys(m.dbpool, m.user)

	case spinner.TickMsg:
		var cmd tea.Cmd
		if m.state < stateNormal {
			m.spinner, cmd = m.spinner.Update(msg)
		}
		return m, cmd
	}

	switch m.state {
	case stateNormal:
		m.createKey = createkey.NewModel(m.cfg, m.dbpool, m.user)
	case stateDeletingKey:
		// If an item is being confirmed for delete, any key (other than the key
		// used for confirmation above) cancels the deletion
		k, ok := msg.(tea.KeyMsg)
		if ok && k.String() != "y" {
			m.state = stateNormal
		}
	}

	m.UpdatePaging(msg)

	m, cmd = updateChildren(msg, m)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func updateChildren(msg tea.Msg, m Model) (Model, tea.Cmd) {
	var cmd tea.Cmd

	switch m.state {
	case stateCreateKey:
		newModel, newCmd := m.createKey.Update(msg)
		createKeyModel, ok := newModel.(createkey.Model)
		if !ok {
			panic("could not perform assertion on posts model")
		}
		m.createKey = createKeyModel
		cmd = newCmd
		if m.createKey.Done {
			m.createKey = createkey.NewModel(m.cfg, m.dbpool, m.user) // reset the state
			m.state = stateNormal
		} else if m.createKey.Quit {
			m.state = stateQuitting
			return m, tea.Quit
		}

	}

	return m, cmd
}

// View renders the current UI into a string.
func (m Model) View() string {
	if m.err != nil {
		return m.err.Error()
	}

	var s string

	switch m.state {
	case stateLoading:
		s = m.spinner.View() + " Loading...\n\n"
	case stateQuitting:
		s = fmt.Sprintf("Thanks for using %s!\n", m.cfg.Domain)
	case stateCreateKey:
		s = m.createKey.View()
	default:
		s = fmt.Sprintf("Here are the keys linked to your %s account.\n\n", m.cfg.Domain)

		// Keys
		s += keysView(m)
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
			s += "\n\n" + helpView(m)
		}
	}

	return s
}

func keysView(m Model) string {
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
		s += m.newStyledKey(m.styles, key, i+start == m.activeKeyIndex).render(state)
	}

	// If there aren't enough keys to fill the view, fill the missing parts
	// with whitespace
	if len(slice) < m.pager.PerPage {
		for i := len(slice); i < keysPerPage; i++ {
			s += "\n\n\n"
		}
	}

	return s
}

func helpView(m Model) string {
	var items []string
	if len(m.keys) > 1 {
		items = append(items, "j/k, ↑/↓: choose")
	}
	if m.pager.TotalPages > 1 {
		items = append(items, "h/l, ←/→: page")
	}
	items = append(items, []string{"x: delete", "n: create", "esc: exit"}...)
	return common.HelpView(items...)
}

func (m Model) promptView(prompt string) string {
	st := m.styles.Delete.Copy().MarginTop(2).MarginRight(1)
	return st.Render(prompt) +
		m.styles.DeleteDim.Render("(y/N)")
}

// LoadKeys returns the command necessary for loading the keys.
func LoadKeys(m Model) tea.Cmd {
	return tea.Batch(
		fetchKeys(m.dbpool, m.user),
		spinner.Tick,
	)
}

// fetchKeys loads the current set of keys via the charm client.
func fetchKeys(dbpool db.DB, user *db.User) tea.Cmd {
	return func() tea.Msg {
		ak, err := dbpool.FindKeysForUser(user)
		if err != nil {
			return errMsg{err}
		}
		return keysLoadedMsg(ak)
	}
}

// unlinkKey deletes the selected key.
func unlinkKey(m Model) tea.Cmd {
	return func() tea.Msg {
		id := m.keys[m.getSelectedIndex()].ID
		err := m.dbpool.RemoveKeys([]string{id})
		if err != nil {
			return errMsg{err}
		}
		return unlinkedKeyMsg(m.index)
	}
}

func deleteAccount(m Model) tea.Cmd {
	return func() tea.Msg {
		id := m.keys[m.getSelectedIndex()].UserID
		m.cfg.Logger.Infof("User (%s) requested account deletion (%s)", m.user.Name, id)
		err := m.dbpool.RemoveUsers([]string{id})
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
