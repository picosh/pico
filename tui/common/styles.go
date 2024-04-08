package common

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/ssh"
	"github.com/kr/pty"
	"github.com/muesli/termenv"
)

// Bridge Wish and Termenv so we can query for a user's terminal capabilities.
type sshOutput struct {
	ssh.Session
	tty *os.File
}

func (s *sshOutput) Write(p []byte) (int, error) {
	return s.Session.Write(p)
}

func (s *sshOutput) Read(p []byte) (int, error) {
	return s.Session.Read(p)
}

func (s *sshOutput) Fd() uintptr {
	return s.tty.Fd()
}

type sshEnviron struct {
	environ []string
}

func (s *sshEnviron) Getenv(key string) string {
	for _, v := range s.environ {
		if strings.HasPrefix(v, key+"=") {
			return v[len(key)+1:]
		}
	}
	return ""
}

func (s *sshEnviron) Environ() []string {
	return s.environ
}

// Create a termenv.Output from the session.
func OutputFromSession(sess ssh.Session) *termenv.Output {
	sshPty, _, _ := sess.Pty()
	_, tty, err := pty.Open()
	if err != nil {
		// TODO: FIX
		log.Fatal(err)
	}
	o := &sshOutput{
		Session: sess,
		tty:     tty,
	}
	environ := sess.Environ()
	environ = append(environ, fmt.Sprintf("TERM=%s", sshPty.Term))
	e := &sshEnviron{environ: environ}
	// We need to use unsafe mode here because the ssh session is not running
	// locally and we already know that the session is a TTY.
	return termenv.NewOutput(o, termenv.WithUnsafe(), termenv.WithEnvironment(e))
}

// Color definitions.
var (
	Indigo       = lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#7571F9"}
	SubtleIndigo = lipgloss.AdaptiveColor{Light: "#7D79F6", Dark: "#514DC1"}
	Cream        = lipgloss.AdaptiveColor{Light: "#FFFDF5", Dark: "#FFFDF5"}
	Fuschia      = lipgloss.AdaptiveColor{Light: "#EE6FF8", Dark: "#EE6FF8"}
	Green        = lipgloss.Color("#04B575")
	Red          = lipgloss.AdaptiveColor{Light: "#FF4672", Dark: "#ED567A"}
	FaintRed     = lipgloss.AdaptiveColor{Light: "#FF6F91", Dark: "#C74665"}
)

type Styles struct {
	Cursor,
	Wrap,
	Paragraph,
	Keyword,
	Code,
	Subtle,
	Error,
	Prompt,
	FocusedPrompt,
	Note,
	NoteDim,
	Delete,
	DeleteDim,
	Label,
	LabelDim,
	ListKey,
	ListDim,
	InactivePagination,
	SelectionMarker,
	SelectedMenuItem,
	Checkmark,
	Logo,
	BlurredButtonStyle,
	FocusedButtonStyle,
	HelpSection,
	HelpDivider,
	Spinner,
	CliPadding,
	CliBorder,
	CliHeader,
	App,
	RoundedBorder lipgloss.Style
	Renderer *lipgloss.Renderer
}

func DefaultStyles(renderer *lipgloss.Renderer) Styles {
	s := Styles{
		Renderer: renderer,
	}

	s.Cursor = renderer.NewStyle().Foreground(Fuschia)
	s.Wrap = renderer.NewStyle().Width(58)
	s.Keyword = renderer.NewStyle().Foreground(Green)
	s.Paragraph = s.Wrap.Copy().Margin(1, 0, 0, 2)
	s.Code = renderer.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#FF4672", Dark: "#ED567A"}).
		Background(lipgloss.AdaptiveColor{Light: "#EBE5EC", Dark: "#2B2A2A"}).
		Padding(0, 1)
	s.Subtle = renderer.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"})
	s.Error = renderer.NewStyle().Foreground(Red)
	s.Prompt = renderer.NewStyle().MarginRight(1).SetString(">")
	s.FocusedPrompt = s.Prompt.Copy().Foreground(Fuschia)
	s.Note = renderer.NewStyle().Foreground(Green)
	s.NoteDim = renderer.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#ABE5D1", Dark: "#2B4A3F"})
	s.Delete = s.Error.Copy()
	s.DeleteDim = renderer.NewStyle().Foreground(FaintRed)
	s.Label = renderer.NewStyle().Foreground(Fuschia)
	s.LabelDim = renderer.NewStyle().Foreground(Indigo)
	s.ListKey = renderer.NewStyle().Foreground(Indigo)
	s.ListDim = renderer.NewStyle().Foreground(SubtleIndigo)
	s.InactivePagination = renderer.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#CACACA", Dark: "#4F4F4F"})
	s.SelectionMarker = renderer.NewStyle().
		Foreground(Fuschia).
		PaddingRight(1).
		SetString(">")
	s.Checkmark = renderer.NewStyle().
		SetString("✔").
		Foreground(Green)
	s.SelectedMenuItem = renderer.NewStyle().Foreground(Fuschia)
	s.Logo = renderer.NewStyle().
		Foreground(Cream).
		Background(Indigo).
		Padding(0, 1)
	s.BlurredButtonStyle = renderer.NewStyle().
		Foreground(Cream).
		Background(lipgloss.AdaptiveColor{Light: "#BDB0BE", Dark: "#827983"}).
		Padding(0, 3)
	s.FocusedButtonStyle = s.BlurredButtonStyle.Copy().
		Background(Fuschia)
	s.HelpDivider = renderer.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#DDDADA", Dark: "#3C3C3C"}).
		Padding(0, 1).
		SetString("•")
	s.HelpSection = renderer.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"})
	s.App = renderer.NewStyle().Margin(1, 0, 1, 2)
	s.Spinner = renderer.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#8E8E8E", Dark: "#747373"})
	s.CliPadding = renderer.NewStyle().Padding(0, 1)
	s.CliHeader = s.CliPadding.Copy().Foreground(Indigo).Bold(true)
	s.CliBorder = renderer.NewStyle().Foreground(lipgloss.Color("238"))
	s.RoundedBorder = renderer.
		NewStyle().
		Padding(0, 1).
		BorderForeground(Indigo).
		Border(lipgloss.RoundedBorder(), true, true)

	return s
}
