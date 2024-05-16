package createkey

import (
	"errors"
	"strings"

	input "github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui/common"
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
type KeyTakenMsg struct{}

type errMsg struct {
	err error
}

func (e errMsg) Error() string { return e.err.Error() }

type Model struct {
	Done bool
	Quit bool

	dbpool db.DB
	user   *db.User
	styles common.Styles
	state  state
	newKey string
	index  index
	errMsg string
	input  input.Model
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
func NewModel(styles common.Styles, dbpool db.DB, user *db.User) Model {
	im := input.New()
	im.Cursor.Style = styles.Cursor
	im.Placeholder = "ssh-ed25519 AAAA..."
	im.Prompt = styles.FocusedPrompt.String()
	im.CharLimit = 2049
	im.Focus()

	return Model{
		Done:   false,
		Quit:   false,
		dbpool: dbpool,
		user:   user,
		styles: styles,
		state:  ready,
		newKey: "",
		index:  textInput,
		errMsg: "",
		input:  im,
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

					return m, addPublicKey(m)
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

	case KeyTakenMsg:
		m.state = ready
		head := m.styles.Error.Render("Invalid public key. ")
		helpMsg := "Public key is associated with another user"
		body := m.styles.Subtle.Render(helpMsg)
		m.errMsg = m.styles.Wrap.Render(head + body)

		return m, nil

	case errMsg:
		m.state = ready
		head := m.styles.Error.Render("Oh, what? There was a curious error we were not expecting. ")
		body := m.styles.Subtle.Render(msg.Error())
		m.errMsg = m.styles.Wrap.Render(head + body)

		return m, nil

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
		s += common.OKButtonView(m.styles, m.index == 1, true)
		s += " " + common.CancelButtonView(m.styles, m.index == 2, false)
		if m.errMsg != "" {
			s += "\n\n" + m.errMsg
		}
	}

	return s
}

func spinnerView(m Model) string {
	return "Submitting..."
}

func addPublicKey(m Model) tea.Cmd {
	return func() tea.Msg {
		pk, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(m.newKey))
		if err != nil {
			return KeyInvalidMsg{}
		}

		key, err := shared.KeyForKeyText(pk)
		if err != nil {
			return KeyInvalidMsg{}
		}
		err = m.dbpool.InsertPublicKey(m.user.ID, key, comment, nil)
		if err != nil {
			if errors.Is(err, db.ErrPublicKeyTaken) {
				return KeyTakenMsg{}
			}
			return errMsg{err}
		}

		return KeySetMsg(m.newKey)
	}
}
