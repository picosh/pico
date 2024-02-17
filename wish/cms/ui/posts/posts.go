package posts

import (
	"errors"
	"log/slog"

	pager "github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/cms/config"
	"github.com/picosh/pico/wish/cms/ui/common"
)

const keysPerPage = 1

type state int

const (
	stateLoading state = iota
	stateNormal
	stateDeletingPost
	stateQuitting
)

type postState int

const (
	postNormal postState = iota
	postSelected
	postDeleting
)

type PostLoader struct {
	Posts []*db.Post
}

type (
	postsLoadedMsg PostLoader
	removePostMsg  int
	errMsg         struct {
		err error
	}
)

// Model is the Tea state model for this user interface.
type Model struct {
	cfg     *config.ConfigCms
	urls    config.ConfigURL
	dbpool  db.DB
	st      storage.StorageServe
	user    *db.User
	posts   []*db.Post
	styles  common.Styles
	pager   pager.Model
	state   state
	err     error
	index   int // index of selected key in relation to the current page
	Exit    bool
	Quit    bool
	spinner spinner.Model
	logger  *slog.Logger
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
	m.pager.SetTotalPages(len(m.posts))
	m.pager, _ = m.pager.Update(msg)

	// If selected item is out of bounds, put it in bounds
	numItems := m.pager.ItemsOnPage(len(m.posts))
	m.index = min(m.index, numItems-1)
}

// NewModel creates a new model with defaults.
func NewModel(cfg *config.ConfigCms, urls config.ConfigURL, dbpool db.DB, user *db.User, stor storage.StorageServe, perPage int) Model {
	logger := cfg.Logger
	st := common.DefaultStyles()

	p := pager.New()
	p.PerPage = keysPerPage
	p.Type = pager.Dots
	p.InactiveDot = st.InactivePagination.Render("•")

	if perPage > 0 {
		p.PerPage = perPage
	}

	return Model{
		cfg:     cfg,
		dbpool:  dbpool,
		st:      stor,
		user:    user,
		styles:  st,
		pager:   p,
		state:   stateLoading,
		err:     nil,
		posts:   []*db.Post{},
		index:   0,
		spinner: common.NewSpinner(),
		Exit:    false,
		Quit:    false,
		logger:  logger,
		urls:    urls,
	}
}

// Init is the Tea initialization function.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
	)
}

// Update is the tea update function which handles incoming messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.Exit = true
			return m, nil

		// Select individual items
		case "up", "k":
			// Move up
			m.index--
			if m.index < 0 && m.pager.Page > 0 {
				m.index = m.pager.PerPage - 1
				m.pager.PrevPage()
			}
			m.index = max(0, m.index)
		case "down", "j":
			// Move down
			itemsOnPage := m.pager.ItemsOnPage(len(m.posts))
			m.index++
			if m.index > itemsOnPage-1 && m.pager.Page < m.pager.TotalPages-1 {
				m.index = 0
				m.pager.NextPage()
			}
			m.index = min(itemsOnPage-1, m.index)

		// Delete
		case "x":
			if len(m.posts) > 0 {
				m.state = stateDeletingPost
				m.UpdatePaging(msg)
			}

			return m, nil

		// Confirm Delete
		case "y":
			switch m.state {
			case stateDeletingPost:
				m.state = stateNormal
				return m, removePost(m)
			}
		}

	case errMsg:
		m.err = msg.err
		return m, nil

	case postsLoadedMsg:
		m.state = stateNormal
		m.index = 0
		m.posts = msg.Posts

	case removePostMsg:
		if m.state == stateQuitting {
			return m, tea.Quit
		}
		i := m.getSelectedIndex()

		// Remove key from array
		m.posts = append(m.posts[:i], m.posts[i+1:]...)

		// Update pagination
		m.pager.SetTotalPages(len(m.posts))
		m.pager.Page = min(m.pager.Page, m.pager.TotalPages-1)

		// Update cursor
		m.index = min(m.index, m.pager.ItemsOnPage(len(m.posts)-1))

		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		if m.state < stateNormal {
			m.spinner, cmd = m.spinner.Update(msg)
		}
		return m, cmd
	}

	m.UpdatePaging(msg)

	// If an item is being confirmed for delete, any key (other than the key
	// used for confirmation above) cancels the deletion
	k, ok := msg.(tea.KeyMsg)
	if ok && k.String() != "x" {
		m.state = stateNormal
	}

	return m, nil
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
		s = "Thanks for using lists!\n"
	default:
		s = "Here are the posts linked to your account.\n\n"

		s += postsView(m)
		if m.pager.TotalPages > 1 {
			s += m.pager.View()
		}

		// Footer
		switch m.state {
		case stateDeletingPost:
			s += m.promptView("Delete this post?")
		default:
			s += "\n\n" + helpView(m)
		}
	}

	return s
}

func postsView(m Model) string {
	var (
		s          string
		state      postState
		start, end = m.pager.GetSliceBounds(len(m.posts))
		slice      = m.posts[start:end]
	)

	destructiveState := m.state == stateDeletingPost

	if len(m.posts) == 0 {
		s += "You don't have any posts yet."
		return s
	}

	// Render key info
	for i, post := range slice {
		if destructiveState && m.index == i {
			state = postDeleting
		} else if m.index == i {
			state = postSelected
		} else {
			state = postNormal
		}
		s += m.newStyledKey(m.styles, post, m.urls).render(state)
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

func helpView(m Model) string {
	var items []string
	if len(m.posts) > 1 {
		items = append(items, "j/k, ↑/↓: choose")
	}
	if m.pager.TotalPages > 1 {
		items = append(items, "h/l, ←/→: page")
	}
	if len(m.posts) > 0 {
		items = append(items, "x: delete")
	}
	items = append(items, "esc: exit")
	return common.HelpView(items...)
}

func (m Model) promptView(prompt string) string {
	st := m.styles.Delete.Copy().MarginTop(2).MarginRight(1)
	return st.Render(prompt) +
		m.styles.DeleteDim.Render("(y/N)")
}

func LoadPosts(m Model) tea.Cmd {
	if m.user == nil {
		m.logger.Info("user not found!")
		err := errors.New("user not found")
		return func() tea.Msg {
			return errMsg{err}
		}
	}

	return tea.Batch(
		m.fetchPosts(m.user.ID),
		m.spinner.Tick,
	)
}

func (m Model) fetchPosts(userID string) tea.Cmd {
	return func() tea.Msg {
		posts, _ := m.dbpool.FindAllPostsForUser(userID, m.cfg.Space)
		loader := PostLoader{
			Posts: posts,
		}
		return postsLoadedMsg(loader)
	}
}

func removePost(m Model) tea.Cmd {
	return func() tea.Msg {
		bucket, err := m.st.UpsertBucket(m.user.ID)
		if err != nil {
			return errMsg{err}
		}

		err = m.st.DeleteObject(bucket, m.posts[m.getSelectedIndex()].Filename)
		if err != nil {
			return errMsg{err}
		}

		err = m.dbpool.RemovePosts([]string{m.posts[m.getSelectedIndex()].ID})
		if err != nil {
			return errMsg{err}
		}

		return removePostMsg(m.index)
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
