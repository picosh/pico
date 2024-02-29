package account

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	input "github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/wish/cms/config"
	"github.com/picosh/pico/wish/cms/ui/common"
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
type CreateModel struct {
	Done bool // true when it's time to exit this view
	Quit bool // true when the user wants to quit the whole program

	cfg       *config.ConfigCms
	dbpool    db.DB
	publicKey string
	styles    common.Styles
	state     state
	newName   string
	index     index
	errMsg    string
	input     input.Model
	spinner   spinner.Model
}

// updateFocus updates the focused states in the model based on the current
// focus index.
func (m *CreateModel) updateFocus() {
	if m.index == textInput && !m.input.Focused() {
		m.input.Focus()
		m.input.Prompt = m.styles.FocusedPrompt.String()
	} else if m.index != textInput && m.input.Focused() {
		m.input.Blur()
		m.input.Prompt = m.styles.Prompt.String()
	}
}

// Move the focus index one unit forward.
func (m *CreateModel) indexForward() {
	m.index++
	if m.index > cancelButton {
		m.index = textInput
	}

	m.updateFocus()
}

// Move the focus index one unit backwards.
func (m *CreateModel) indexBackward() {
	m.index--
	if m.index < textInput {
		m.index = cancelButton
	}

	m.updateFocus()
}

// NewModel returns a new username model in its initial state.
func NewCreateModel(styles common.Styles, cfg *config.ConfigCms, dbpool db.DB, publicKey string) CreateModel {
	im := input.New()
	im.Cursor.Style = styles.Cursor
	im.Placeholder = "enter username"
	im.Prompt = styles.FocusedPrompt.String()
	im.CharLimit = 50
	im.Focus()

	return CreateModel{
		cfg:       cfg,
		Done:      false,
		Quit:      false,
		dbpool:    dbpool,
		styles:    styles,
		state:     ready,
		newName:   "",
		index:     textInput,
		errMsg:    "",
		input:     im,
		spinner:   common.NewSpinner(styles),
		publicKey: publicKey,
	}
}

// Init is the Bubble Tea initialization function.
func Init(styles common.Styles, cfg *config.ConfigCms, dbpool db.DB, publicKey string) func() (CreateModel, tea.Cmd) {
	return func() (CreateModel, tea.Cmd) {
		m := NewCreateModel(styles, cfg, dbpool, publicKey)
		return m, InitialCmd()
	}
}

// InitialCmd returns the initial command.
func InitialCmd() tea.Cmd {
	return input.Blink
}

// Update is the Bubble Tea update loop.
func Update(msg tea.Msg, m CreateModel) (CreateModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEscape:
			m.Quit = true
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
					m.newName = strings.TrimSpace(m.input.Value())

					return m, tea.Batch(
						createAccount(m), // fire off the command, too
						m.spinner.Tick,
					)
				case cancelButton: // Exit
					m.Quit = true
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

	case NameTakenMsg:
		m.state = ready
		m.errMsg = m.styles.Subtle.Render("Sorry, ") +
			m.styles.Error.Render(m.newName) +
			m.styles.Subtle.Render(" is taken.")

		return m, nil

	case NameInvalidMsg:
		m.state = ready
		head := m.styles.Error.Render("Invalid name. ")
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
func View(m CreateModel) string {
	if !m.cfg.AllowRegister {
		return "Registration is closed for this service.  Press 'esc' to exit."
	}

	s := fmt.Sprintf("%s\n\n%s\n", "hacker labs", m.cfg.IntroText)
	s += fmt.Sprintf("Public Key: %s\n\n", m.publicKey)
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
	s += fmt.Sprintf("\n\n%s\n", helpMsg)

	return s
}

func spinnerView(m CreateModel) string {
	return m.spinner.View() + " Creating account..."
}

func createAccount(m CreateModel) tea.Cmd {
	return func() tea.Msg {
		if m.newName == "" {
			return NameInvalidMsg{}
		}

		user, err := m.dbpool.RegisterUser(m.newName, m.publicKey)
		fmt.Println(err)
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
