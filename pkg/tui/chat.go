package tui

import (
	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/button"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
)

type ChatPage struct {
	shared *SharedModel
	btn    *button.Button
}

func NewChatPage(shrd *SharedModel) *ChatPage {
	btn := button.New("chat!", func() (vxfw.Command, error) { return nil, nil })
	btn.Style = button.StyleSet{
		Default: vaxis.Style{Background: oj, Foreground: black},
	}
	return &ChatPage{shared: shrd, btn: btn}
}

func (m *ChatPage) Footer() []Shortcut {
	short := []Shortcut{
		{Shortcut: "enter", Text: "chat"},
	}
	return short
}

func (m *ChatPage) hasAccess() bool {
	if m.shared.PlusFeatureFlag != nil && m.shared.PlusFeatureFlag.IsValid() {
		return true
	}

	if m.shared.BouncerFeatureFlag != nil && m.shared.BouncerFeatureFlag.IsValid() {
		return true
	}

	return false
}

func (m *ChatPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		return vxfw.FocusWidgetCmd(m), nil
	case vaxis.Key:
		if msg.Matches(vaxis.KeyEnter) {
			_ = m.shared.App.Suspend()
			loadChat(m.shared)
			_ = m.shared.App.Resume()
			return vxfw.QuitCmd{}, nil
		}
	}

	return nil, nil
}

func (m *ChatPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	segs := []vaxis.Segment{
		{Text: "We provide a managed IRC bouncer for pico+ users.  When you click the button we will open our TUI chat with your user authenticated automatically.\n\n"},
		{Text: "If you haven't configured your pico+ account with our IRC bouncer, the guide is here:\n\n  "},
		{Text: "https://pico.sh/bouncer", Style: vaxis.Style{Hyperlink: "https://pico.sh/bouncer"}},
		{Text: "\n\nIf you want to quickly chat with us on IRC without pico+, go to the web chat:\n\n  "},
		{Text: "https://web.libera.chat/gamja?autojoin=#pico.sh", Style: vaxis.Style{Hyperlink: "https://web.libera.chat/gamja?autojoin=#pico.sh"}},
	}
	txt, _ := richtext.New(segs).Draw(ctx)

	surfs := []vxfw.Surface{txt}
	if m.hasAccess() {
		btnSurf, _ := m.btn.Draw(vxfw.DrawContext{
			Characters: ctx.Characters,
			Max:        vxfw.Size{Width: 7, Height: 1},
		})
		surfs = append(surfs, btnSurf)
	} else {
		t := text.New("Our IRC Bouncer is only available to pico+ users.")
		t.Style = vaxis.Style{Foreground: red}
		ss, _ := t.Draw(ctx)
		surfs = append(surfs, ss)
	}
	stack := NewGroupStack(surfs)
	stack.Gap = 1

	brd := NewBorder(stack)
	brd.Label = "irc chat"
	surf, _ := brd.Draw(ctx)

	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)
	root.AddChild(0, 0, surf)
	return root, nil
}
