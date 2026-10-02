package tui

import (
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/vxfw"
	"go.rockorager.dev/vaxis/vxfw/text"
	"go.rockorager.dev/vaxis/vxfw/textfield"
)

type TextInput struct {
	input *textfield.TextField
	label string
	focus bool
}

func (m *TextInput) GetValue() string {
	return m.input.Value
}

func (m *TextInput) Reset() {
	m.input.Reset()
}

func NewTextInput(label string) *TextInput {
	input := textfield.New()
	return &TextInput{
		label: label,
		input: input,
	}
}

func (m *TextInput) FocusIn() (vxfw.Command, error) {
	m.focus = true
	return vxfw.FocusWidgetCmd(m.input), nil
}

func (m *TextInput) FocusOut() (vxfw.Command, error) {
	m.focus = false
	return vxfw.RedrawCmd{}, nil
}

func (m *TextInput) Focused() bool {
	return m.focus
}

func (m *TextInput) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (m *TextInput) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	txt := text.New("> ")
	if m.focus {
		txt.Style = vaxis.Style{Foreground: oj}
	} else {
		txt.Style = vaxis.Style{Foreground: purp}
	}
	txtSurf, _ := txt.Draw(ctx)
	inputSurf, _ := m.input.Draw(ctx)
	stack := NewGroupStack([]vxfw.Surface{
		txtSurf,
		inputSurf,
	})
	stack.Direction = "horizontal"
	brd := NewBorder(stack)
	brd.Label = m.label
	if m.focus {
		brd.Style = vaxis.Style{Foreground: oj}
	} else {
		brd.Style = vaxis.Style{Foreground: purp}
	}
	surf, _ := brd.Draw(ctx)
	return surf, nil
}
