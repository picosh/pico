package createkey

import (
	"strings"

	"git.sr.ht/~erock/pico/wish/cms/config"
	"git.sr.ht/~erock/pico/wish/cms/db"
	"git.sr.ht/~erock/pico/wish/cms/ui/common"
	"github.com/charmbracelet/bubbles/spinner"
	input "github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/crypto/ssh"
)

type state int

const (
	ready state = iota
	submitting
)

type index int

const (
	textInput index = iota
	okButton
	cancelButton
)

type KeySetMsg string

type KeyInvalidMsg struct{}

type errMsg struct {
	err error
}

func (e errMsg) Error() string { return e.err.Error() }

type Model struct {
	Done bool
	Quit bool

	dbpool  db.DB
	user    *db.User
	styles  common.Styles
	state   state
	newKey  string
	index   index
	errMsg  string
	input   input.Model
	spinner spinner.Model
}

// updateFocus updates the focused states in the model based on the current
// focus index.
func (m *Model) updateFocus() {
	if m.index == textInput && !m.input.Focused() {
		m.input.Focus()
		m.input.Prompt = m.styles.FocusedPrompt.String()
	} else if m.index != textInput && m.input.Focused() {
		m.input.Blur()
		m.input.Prompt = m.styles.Prompt.String()
	}
}

// Move the focus index one unit forward.
func (m *Model) indexForward() {
	m.index++
	if m.index > cancelButton {
		m.index = textInput
	}

	m.updateFocus()
}

// Move the focus index one unit backwards.
func (m *Model) indexBackward() {
	m.index--
	if m.index < textInput {
		m.index = cancelButton
	}

	m.updateFocus()
}

// NewModel returns a new username model in its initial state.
func NewModel(cfg *config.ConfigCms, dbpool db.DB, user *db.User) Model {
	st := common.DefaultStyles()

	im := input.NewModel()
	im.CursorStyle = st.Cursor
	im.Placeholder = "ssh-ed25519 AAAA..."
	im.Prompt = st.FocusedPrompt.String()
	im.CharLimit = 2049
	im.Focus()

	return Model{
		Done:    false,
		Quit:    false,
		dbpool:  dbpool,
		user:    user,
		styles:  st,
		state:   ready,
		newKey:  "",
		index:   textInput,
		errMsg:  "",
		input:   im,
		spinner: common.NewSpinner(),
	}
}

// Init is the Bubble Tea initialization function.
func (m Model) Init() tea.Cmd {
	return input.Blink
}

// Update is the Bubble Tea update loop.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC: // quit
			m.Quit = true
			return m, nil
		case tea.KeyEscape: // exit this mini-app
			m.Done = true
			return m, nil

		default:
			// Ignore keys if we're submitting
			if m.state == submitting {
				return m, nil
			}

			switch msg.String() {
			case "tab":
				m.indexForward()
			case "shift+tab":
				m.indexBackward()
			case "l", "k", "right":
				if m.index != textInput {
					m.indexForward()
				}
			case "h", "j", "left":
				if m.index != textInput {
					m.indexBackward()
				}
			case "up", "down":
				if m.index == textInput {
					m.indexForward()
				} else {
					m.index = textInput
					m.updateFocus()
				}
			case "enter":
				switch m.index {
				case textInput:
					fallthrough
				case okButton: // Submit the form
					m.state = submitting
					m.errMsg = ""
					m.newKey = strings.TrimSpace(m.input.Value())

					return m, tea.Batch(
						addPublicKey(m), // fire off the command, too
						spinner.Tick,
					)
				case cancelButton: // Exit this mini-app
					m.Done = true
					return m, nil
				}
			}

			// Pass messages through to the input element if that's the element
			// in focus
			if m.index == textInput {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)

				return m, cmd
			}

			return m, nil
		}

	case KeyInvalidMsg:
		m.state = ready
		head := m.styles.Error.Render("Invalid public key. ")
		helpMsg := "Public keys must but in the correct format"
		body := m.styles.Subtle.Render(helpMsg)
		m.errMsg = m.styles.Wrap.Render(head + body)

		return m, nil

	case errMsg:
		m.state = ready
		head := m.styles.Error.Render("Oh, what? There was a curious error we were not expecting. ")
		body := m.styles.Subtle.Render(msg.Error())
		m.errMsg = m.styles.Wrap.Render(head + body)

		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)

		return m, cmd

	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg) // Do we still need this?

		return m, cmd
	}
}

// View renders current view from the model.
func (m Model) View() string {
	s := "Enter a new public key\n\n"
	s += m.input.View() + "\n\n"

	if m.state == submitting {
		s += spinnerView(m)
	} else {
		s += common.OKButtonView(m.index == 1, true)
		s += " " + common.CancelButtonView(m.index == 2, false)
		if m.errMsg != "" {
			s += "\n\n" + m.errMsg
		}
	}

	return s
}

func spinnerView(m Model) string {
	return m.spinner.View() + " Submitting..."
}

func IsPublicKeyValid(key string) bool {
	if len(key) == 0 {
		return false
	}

	_, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key))
	return err == nil
}

func sanitizeKey(key string) string {
	// comments are removed when using our ssh app so
	// we need to be sure to remove them from the public key
	parts := strings.Split(key, " ")
	keep := []string{}
	for i, part := range parts {
		if i == 2 {
			break
		}
		keep = append(keep, strings.Trim(part, " "))
	}

	return strings.Join(keep, " ")
}

func addPublicKey(m Model) tea.Cmd {
	return func() tea.Msg {
		if !IsPublicKeyValid(m.newKey) {
			return KeyInvalidMsg{}
		}

		key := sanitizeKey(m.newKey)
		err := m.dbpool.LinkUserKey(m.user.ID, key)
		if err != nil {
			return errMsg{err}
		}

		return KeySetMsg(m.newKey)
	}
}
