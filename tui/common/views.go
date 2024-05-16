package common

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// State is a general UI state used to help style components.
type State int

// UI states.
const (
	StateNormal State = iota
	StateSelected
	StateActive
	StateSpecial
	StateDeleting
)

var lineColors = map[State]lipgloss.TerminalColor{
	StateNormal:   lipgloss.AdaptiveColor{Light: "#BCBCBC", Dark: "#646464"},
	StateSelected: lipgloss.Color("#F684FF"),
	StateDeleting: lipgloss.AdaptiveColor{Light: "#FF8BA7", Dark: "#893D4E"},
	StateSpecial:  lipgloss.Color("#04B575"),
}

// VerticalLine return a vertical line colored according to the given state.
func VerticalLine(renderer *lipgloss.Renderer, state State) string {
	return renderer.NewStyle().
		SetString("│").
		Foreground(lineColors[state]).
		String()
}

// KeyValueView renders key-value pairs.
func KeyValueView(styles Styles, stuff ...string) string {
	if len(stuff) == 0 {
		return ""
	}

	var (
		s     string
		index int
	)
	for i := 0; i < len(stuff); i++ {
		if i%2 == 0 {
			// even: key
			s += fmt.Sprintf("%s %s: ", VerticalLine(styles.Renderer, StateNormal), stuff[i])
			continue
		}
		// odd: value
		s += styles.Label.Render(stuff[i])
		s += "\n"
		index++
	}

	return strings.TrimSpace(s)
}

// OKButtonView returns a button reading "OK".
func OKButtonView(styles Styles, focused bool, defaultButton bool) string {
	return styledButton(styles, "OK", defaultButton, focused)
}

// CancelButtonView returns a button reading "Cancel.".
func CancelButtonView(styles Styles, focused bool, defaultButton bool) string {
	return styledButton(styles, "Cancel", defaultButton, focused)
}

func styledButton(styles Styles, str string, underlined, focused bool) string {
	var st lipgloss.Style
	if focused {
		st = styles.FocusedButtonStyle.Copy()
	} else {
		st = styles.BlurredButtonStyle.Copy()
	}
	if underlined {
		st = st.Underline(true)
	}
	return st.Render(str)
}

// HelpView renders text intended to display at help text, often at the
// bottom of a view.
func HelpView(styles Styles, sections ...string) string {
	var s string
	if len(sections) == 0 {
		return s
	}

	for i := 0; i < len(sections); i++ {
		s += styles.HelpSection.Render(sections[i])
		if i < len(sections)-1 {
			s += styles.HelpDivider.Render()
		}
	}

	return s
}

func LogoView() string {
	return `
    .                   ."
   i-~l^             'I~??!
  I??_??-<I^     .,!_??+<-?I
  _-+ .,!+??->:;<??-<;'  +-_
 '-?i     ':i_??_!".     i?-'
  _-+         ''         +-_
  I??I                  I??I
   !??l.              .l??i
    ;_?_I'          'I_?_;
     .I+??_>l:,,:l>_??+I.
        ';i+--??--+i;'
             ....`
}
