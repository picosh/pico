package createtoken

import (
	"fmt"
	"strings"

	input "github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/pages"
)

type state int

const (
	ready state = iota
	submitting
	submitted
)

type index int

const (
	textInput index = iota
	okButton
	cancelButton
)

type TokenSetMsg struct {
	token string
}

type errMsg struct {
	err error
}

func (e errMsg) Error() string { return e.err.Error() }

type Model struct {
	shared common.SharedModel

	state     state
	tokenName string
	token     string
	index     index
	errMsg    string
	input     input.Model
}

// NewModel returns a new username model in its initial state.
func NewModel(shared common.SharedModel) Model {
	im := input.New()
	im.Cursor.Style = shared.Styles.Cursor
	im.Placeholder = "A name used for your reference"
	im.Prompt = shared.Styles.FocusedPrompt.String()
	im.CharLimit = 256
	im.Focus()

	return Model{
		shared: shared,

		state:     ready,
		tokenName: "",
		token:     "",
		index:     textInput,
		errMsg:    "",
		input:     im,
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
	m.index += 1
	if m.index > cancelButton {
		m.index = textInput
	}

	m.updateFocus()
}

// Move the focus index one unit backwards.
func (m *Model) indexBackward() {
	m.index -= 1
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
		case "q", "esc":
			return m, pages.Navigate(pages.TokensPage)
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
				// form already submitted so ok button exits
				if m.state == submitted {
					return m, pages.Navigate(pages.TokensPage)
				}

				m.state = submitting
				m.errMsg = ""
				m.tokenName = strings.TrimSpace(m.input.Value())

				return m, m.addToken()
			case cancelButton:
				return m, pages.Navigate(pages.TokensPage)
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

	case TokenSetMsg:
		m.state = submitted
		m.token = msg.token
		return m, nil

	case errMsg:
		m.state = ready
		head := m.shared.Styles.Error.Render("Oh, what? There was a curious error we were not expecting. ")
		body := m.shared.Styles.Subtle.Render(msg.Error())
		m.errMsg = m.shared.Styles.Wrap.Render(head + body)

		return m, nil

	// leaving page so reset model
	case pages.NavigateMsg:
		next := NewModel(m.shared)
		return next, next.Init()

	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg) // Do we still need this?

		return m, cmd
	}
}

// View renders current view from the model.
func (m Model) View() string {
	s := "Enter a name for your token\n\n"
	s += m.input.View() + "\n\n"

	if m.state == submitting {
		s += spinnerView()
	} else if m.state == submitted {
		s = fmt.Sprintf("Save this token:\n%s\n\n", m.token)
		s += "After you exit this screen you will *not* be able to see it again.\n\n"
		s += common.OKButtonView(m.shared.Styles, m.index == 1, true)
	} else {
		s += common.OKButtonView(m.shared.Styles, m.index == 1, true)
		s += " " + common.CancelButtonView(m.shared.Styles, m.index == 2, false)
		if m.errMsg != "" {
			s += "\n\n" + m.errMsg
		}
	}

	return s
}

func (m *Model) addToken() tea.Cmd {
	return func() tea.Msg {
		token, err := m.shared.Dbpool.InsertToken(m.shared.User.ID, m.tokenName)
		if err != nil {
			return errMsg{err}
		}

		return TokenSetMsg{token}
	}
}

func spinnerView() string {
	return "Submitting..."
}
