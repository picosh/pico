package createtoken

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	input "github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
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

type TokenDismissed int

type TokenSetMsg struct {
	token string
}

type errMsg struct {
	err error
}

func (e errMsg) Error() string { return e.err.Error() }

type Model struct {
	Done bool
	Quit bool

	dbpool    db.DB
	user      *db.User
	styles    common.Styles
	state     state
	tokenName string
	token     string
	index     index
	errMsg    string
	input     input.Model
	spinner   spinner.Model
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
	im.Placeholder = "A name used for your reference"
	im.Prompt = styles.FocusedPrompt.String()
	im.CharLimit = 256
	im.Focus()

	return Model{
		Done:      false,
		Quit:      false,
		dbpool:    dbpool,
		user:      user,
		styles:    styles,
		state:     ready,
		tokenName: "",
		token:     "",
		index:     textInput,
		errMsg:    "",
		input:     im,
		spinner:   common.NewSpinner(styles),
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
			return m, dismiss
		case tea.KeyEscape: // exit this mini-app
			m.Done = true
			return m, dismiss

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
					// form already submitted so ok button exits
					if m.state == submitted {
						m.Done = true
						return m, dismiss
					}

					m.state = submitting
					m.errMsg = ""
					m.tokenName = strings.TrimSpace(m.input.Value())

					return m, tea.Batch(
						addToken(m), // fire off the command, too
						m.spinner.Tick,
					)
				case cancelButton: // Exit this mini-app
					m.Done = true
					return m, dismiss
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

	case TokenSetMsg:
		m.state = submitted
		m.token = msg.token
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
	s := "Enter a name for your token\n\n"
	s += m.input.View() + "\n\n"

	if m.state == submitting {
		s += spinnerView(m)
	} else if m.state == submitted {
		s = fmt.Sprintf("Save this token:\n%s\n\n", m.token)
		s += "After you exit this screen you will *not* be able to see it again.\n\n"
		s += common.OKButtonView(m.styles, m.index == 1, true)
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
	return m.spinner.View() + " Submitting..."
}

func dismiss() tea.Msg { return TokenDismissed(1) }

func addToken(m Model) tea.Cmd {
	return func() tea.Msg {
		token, err := m.dbpool.InsertToken(m.user.ID, m.tokenName)
		if err != nil {
			return errMsg{err}
		}

		return TokenSetMsg{token}
	}
}
