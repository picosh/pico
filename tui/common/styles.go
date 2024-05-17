package common

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	Indigo       = lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#7571F9"}
	SubtleIndigo = lipgloss.AdaptiveColor{Light: "#7D79F6", Dark: "#514DC1"}
	Cream        = lipgloss.AdaptiveColor{Light: "#FFFDF5", Dark: "#FFFDF5"}
	Fuschia      = lipgloss.AdaptiveColor{Light: "#EE6FF8", Dark: "#EE6FF8"}
	Green        = lipgloss.AdaptiveColor{Light: "#ABE5D1", Dark: "#04B575"}
	DarkRed      = lipgloss.AdaptiveColor{Light: "#EBE5EC", Dark: "#2B2A2A"}
	Red          = lipgloss.AdaptiveColor{Light: "#FF4672", Dark: "#ED567A"}
	FaintRed     = lipgloss.AdaptiveColor{Light: "#FF6F91", Dark: "#C74665"}
	Grey         = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}
	GreyLight    = lipgloss.AdaptiveColor{Light: "#BDB0BE", Dark: "#827983"}
)

type Styles struct {
	Cursor,
	Wrap,
	Paragraph,
	Code,
	Subtle,
	Error,
	Prompt,
	FocusedPrompt,
	Note,
	Delete,
	Label,
	ListKey,
	InactivePagination,
	SelectionMarker,
	SelectedMenuItem,
	Logo,
	BlurredButtonStyle,
	FocusedButtonStyle,
	HelpSection,
	HelpDivider,
	App,
	InputPlaceholder,
	RoundedBorder lipgloss.Style
	Renderer *lipgloss.Renderer
}

func DefaultStyles(renderer *lipgloss.Renderer) Styles {
	s := Styles{
		Renderer: renderer,
	}

	s.Cursor = renderer.NewStyle().Foreground(Fuschia)
	s.Wrap = renderer.NewStyle().Width(58)
	s.Paragraph = s.Wrap.Copy().Margin(1, 0, 0, 2)
	s.Logo = renderer.NewStyle().
		Foreground(Cream).
		Background(Indigo).
		Padding(0, 1)
	s.Code = renderer.NewStyle().
		Foreground(Red).
		Background(DarkRed).
		Padding(0, 1)
	s.Subtle = renderer.NewStyle().
		Foreground(Grey)
	s.Error = renderer.NewStyle().Foreground(Red)
	s.Prompt = renderer.NewStyle().MarginRight(1).SetString(">")
	s.FocusedPrompt = s.Prompt.Copy().Foreground(Fuschia)
	s.InputPlaceholder = renderer.NewStyle().Foreground(Grey)
	s.Note = renderer.NewStyle().Foreground(Green)
	s.Delete = s.Error.Copy()
	s.Label = renderer.NewStyle().Foreground(Fuschia)
	s.ListKey = renderer.NewStyle().Foreground(Indigo)
	s.InactivePagination = renderer.NewStyle().
		Foreground(Grey)
	s.SelectionMarker = renderer.NewStyle().
		Foreground(Fuschia).
		PaddingRight(1).
		SetString("•")
	s.SelectedMenuItem = renderer.NewStyle().Foreground(Fuschia)
	s.BlurredButtonStyle = renderer.NewStyle().
		Foreground(Cream).
		Background(GreyLight).
		Padding(0, 3)
	s.FocusedButtonStyle = s.BlurredButtonStyle.Copy().
		Background(Fuschia)
	s.HelpDivider = renderer.NewStyle().
		Foreground(Grey).
		Padding(0, 1).
		SetString("•")
	s.HelpSection = renderer.NewStyle().
		Foreground(Grey)
	s.App = renderer.NewStyle().Margin(1, 0, 1, 2)
	s.RoundedBorder = renderer.
		NewStyle().
		Padding(0, 1).
		BorderForeground(Indigo).
		Border(lipgloss.RoundedBorder(), true, true)

	return s
}
