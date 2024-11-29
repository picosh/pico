package createaccount

import (
	"errors"
	"fmt"
	"strings"

	input "github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/utils"
)

type state int

const (
	ready state = iota
	submitting
)

// index specifies the UI element that's in focus.
type index int

const (
	textInput index = iota
	okButton
	cancelButton
)

type CreateAccountMsg *db.User

// NameTakenMsg is sent when the requested username has already been taken.
type NameTakenMsg struct{}

// NameInvalidMsg is sent when the requested username has failed validation.
type NameInvalidMsg struct{}

type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

var deny = strings.Join(db.DenyList, ", ")
var helpMsg = fmt.Sprintf("Names can only contain plain letters and numbers and must be less than 50 characters. No emjois. No names from deny list: %s", deny)

// Model holds the state of the username UI.
type Model struct {
	shared *common.SharedModel

	state   state
	newName string
	index   index
	errMsg  string
	input   input.Model
}

// NewModel returns a new username model in its initial state.
func NewModel(shared *common.SharedModel) Model {
	im := input.New()
	im.Cursor.Style = shared.Styles.Cursor
	im.Placeholder = "enter username"
	im.PlaceholderStyle = shared.Styles.InputPlaceholder
	im.Prompt = shared.Styles.FocusedPrompt.String()
	im.CharLimit = 50
	im.Focus()

	return Model{
		shared:  shared,
		state:   ready,
		newName: "",
		index:   textInput,
		errMsg:  "",
		input:   im,
	}
}

// updateFocus updates the focused states in the model based on the current
// focus index.
func (m *Model) updateFocus() {
	if m.index == textInput && !m.input.Focused() {
		m.input.Focus()
		m.input.Prompt = m.shared.Styles.FocusedPrompt.String()
	} else if m.index != textInput && m.input.Focused() {
		m.input.Blur()
		m.input.Prompt = m.shared.Styles.Prompt.String()
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

// Init is the Bubble Tea initialization function.
func (m Model) Init() tea.Cmd {
	return input.Blink
}

// Update is the Bubble Tea update loop.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
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
				m.newName = strings.TrimSpace(m.input.Value())

				return m, m.createAccount()
			case cancelButton: // Exit
				return m, tea.Quit
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

	case NameTakenMsg:
		m.state = ready
		m.errMsg = m.shared.Styles.Subtle.Render("Sorry, ") +
			m.shared.Styles.Error.Render(m.newName) +
			m.shared.Styles.Subtle.Render(" is taken.")

		return m, nil

	case NameInvalidMsg:
		m.state = ready
		head := m.shared.Styles.Error.Render("Invalid name.")
		m.errMsg = m.shared.Styles.Wrap.Render(head)

		return m, nil

	case errMsg:
		m.state = ready
		head := m.shared.Styles.Error.Render("Oh, what? There was a curious error we were not expecting. ")
		body := m.shared.Styles.Subtle.Render(msg.Error())
		m.errMsg = m.shared.Styles.Wrap.Render(head + body)

		return m, nil

	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg) // Do we still need this?

		return m, cmd
	}
}

// View renders current view from the model.
func (m Model) View() string {
	s := common.LogoView() + "\n\n"
	pubkey := fmt.Sprintf("pubkey: %s", utils.KeyForSha256(m.shared.Session.PublicKey()))
	s += m.shared.Styles.Label.SetString(pubkey).String()
	s += "\n\n" + m.input.View() + "\n\n"

	if m.state == submitting {
		s += m.spinnerView()
	} else {
		s += common.OKButtonView(m.shared.Styles, m.index == 1, true)
		s += " " + common.CancelButtonView(m.shared.Styles, m.index == 2, false)
		if m.errMsg != "" {
			s += "\n\n" + m.errMsg
		}
	}
	s += fmt.Sprintf("\n\n%s\n", m.shared.Styles.HelpSection.SetString(helpMsg))

	return s
}

func (m Model) spinnerView() string {
	return "Creating account..."
}

func (m *Model) createAccount() tea.Cmd {
	return func() tea.Msg {
		if m.newName == "" {
			return NameInvalidMsg{}
		}

		key := utils.KeyForKeyText(m.shared.Session.PublicKey())

		user, err := m.shared.Dbpool.RegisterUser(m.newName, key, "")
		if err != nil {
			if errors.Is(err, db.ErrNameTaken) {
				return NameTakenMsg{}
			} else if errors.Is(err, db.ErrNameInvalid) {
				return NameInvalidMsg{}
			} else if errors.Is(err, db.ErrNameDenied) {
				return NameInvalidMsg{}
			} else {
				return errMsg{err}
			}
		}

		return CreateAccountMsg(user)
	}
}
