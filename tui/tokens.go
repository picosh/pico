package tui

import (
	"fmt"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/button"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/db"
)

type TokensPage struct {
	shared *SharedModel
	list   list.Dynamic

	tokens  []*db.Token
	err     error
	confirm bool
}

func NewTokensPage(shrd *SharedModel) *TokensPage {
	m := &TokensPage{
		shared: shrd,
	}
	m.list = list.Dynamic{DrawCursor: true, Builder: m.getWidget}
	return m
}

type FetchTokens struct{}

func (m *TokensPage) Footer() []Shortcut {
	return []Shortcut{
		{Shortcut: "j/k", Text: "choose"},
		{Shortcut: "x", Text: "delete"},
		{Shortcut: "c", Text: "create"},
	}
}

func (m *TokensPage) fetchTokens() error {
	tokens, err := m.shared.Dbpool.FindTokensForUser(m.shared.User.ID)
	if err != nil {
		return err

	}
	m.tokens = tokens
	return nil
}

func (m *TokensPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		m.err = m.fetchTokens()
		return vxfw.FocusWidgetCmd(&m.list), nil
	case vaxis.Key:
		if msg.Matches('c') {
			m.shared.App.PostEvent(Navigate{To: "add-token"})
		}
		if msg.Matches('x') {
			m.confirm = true
			return vxfw.RedrawCmd{}, nil
		}
		if msg.Matches('y') {
			if m.confirm {
				m.confirm = false
				err := m.shared.Dbpool.RemoveToken(m.tokens[m.list.Cursor()].ID)
				if err != nil {
					m.err = err
					return nil, nil
				}
				m.err = m.fetchTokens()
				return vxfw.RedrawCmd{}, nil
			}
		}
		if msg.Matches('n') {
			m.confirm = false
			return vxfw.RedrawCmd{}, nil
		}
	}

	return nil, nil
}

func (m *TokensPage) getWidget(i uint, cursor uint) vxfw.Widget {
	if int(i) >= len(m.tokens) {
		return nil
	}

	style := vaxis.Style{Foreground: grey}
	isSelected := i == cursor
	if isSelected {
		style = vaxis.Style{Foreground: fuschia}
	}

	token := m.tokens[i]
	txt := richtext.New([]vaxis.Segment{
		{Text: "Name: ", Style: style},
		{Text: token.Name + "\n"},

		{Text: "Created: ", Style: style},
		{Text: token.CreatedAt.Format(time.DateOnly)},
	})

	return txt
}

func (m *TokensPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	root := vxfw.NewSurface(w, h, m)

	header := richtext.New([]vaxis.Segment{
		{
			Text: fmt.Sprintf(
				"%d tokens\n",
				len(m.tokens),
			),
		},
	})
	headerSurf, _ := header.Draw(createDrawCtx(ctx, 2))
	root.AddChild(0, 0, headerSurf)

	listSurf, _ := m.list.Draw(createDrawCtx(ctx, h-5))
	root.AddChild(0, 3, listSurf)

	segs := []vaxis.Segment{}
	if m.confirm {
		segs = append(segs, vaxis.Segment{
			Text:  "are you sure? y/n\n",
			Style: vaxis.Style{Foreground: red},
		})
	}
	if m.err != nil {
		segs = append(segs, vaxis.Segment{
			Text:  m.err.Error() + "\n",
			Style: vaxis.Style{Foreground: red},
		})
	}
	segs = append(segs, vaxis.Segment{Text: "\n"})

	footer := richtext.New(segs)
	footerSurf, _ := footer.Draw(createDrawCtx(ctx, 3))
	root.AddChild(0, int(h)-3, footerSurf)

	return root, nil
}

type AddTokenPage struct {
	shared *SharedModel

	token string
	err   error
	focus string
	input *TextInput
	btn   *button.Button
}

func NewAddTokenPage(shrd *SharedModel) *AddTokenPage {
	btn := button.New("ADD", func() (vxfw.Command, error) { return nil, nil })
	btn.Style = button.StyleSet{
		Default: vaxis.Style{Background: grey},
		Focus:   vaxis.Style{Background: oj, Foreground: black},
	}
	return &AddTokenPage{
		shared: shrd,

		input: NewTextInput("enter name"),
		btn:   btn,
	}
}

func (m *AddTokenPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		m.focus = "input"
		m.input.Reset()
		return m.input.FocusIn()
	case vaxis.Key:
		if msg.Matches(vaxis.KeyTab) {
			if m.token != "" {
				return nil, nil
			}
			if m.focus == "input" {
				m.focus = "button"
				cmd, _ := m.input.FocusOut()
				return vxfw.BatchCmd([]vxfw.Command{
					cmd,
					vxfw.FocusWidgetCmd(m.btn),
				}), nil
			}
			m.focus = "input"
			return m.input.FocusIn()
		}
		if msg.Matches(vaxis.KeyEnter) {
			if m.focus == "button" {
				if m.token != "" {
					m.token = ""
					m.err = nil
					m.input.Reset()
					m.shared.App.PostEvent(Navigate{To: "tokens"})
					return vxfw.RedrawCmd{}, nil
				}
				token, err := m.addToken(m.input.GetValue())
				m.token = token
				m.err = err
				return vxfw.RedrawCmd{}, nil
			}
		}
	}

	return nil, nil
}

func (m *AddTokenPage) addToken(name string) (string, error) {
	return m.shared.Dbpool.InsertToken(m.shared.User.ID, name)
}

func (m *AddTokenPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	root := vxfw.NewSurface(w, h, m)

	if m.token == "" {
		header := text.New("Enter a name for the token")
		headerSurf, _ := header.Draw(ctx)
		root.AddChild(0, 0, headerSurf)

		inputSurf, _ := m.input.Draw(createDrawCtx(ctx, 4))
		root.AddChild(0, 3, inputSurf)

		btnSurf, _ := m.btn.Draw(vxfw.DrawContext{
			Characters: ctx.Characters,
			Max:        vxfw.Size{Width: 5, Height: 1},
		})
		root.AddChild(0, 6, btnSurf)
	} else {
		header := text.New(
			fmt.Sprintf(
				"Save this token: %s\n\nAfter you exit this screen you will *not* be able to see it again.",
				m.token,
			),
		)
		headerSurf, _ := header.Draw(ctx)
		root.AddChild(0, 0, headerSurf)

		btnSurf, _ := m.btn.Draw(vxfw.DrawContext{
			Characters: ctx.Characters,
			Max:        vxfw.Size{Width: 5, Height: 1},
		})
		root.AddChild(0, 7, btnSurf)
	}

	if m.err != nil {
		e := text.New(m.err.Error())
		e.Style = vaxis.Style{Foreground: red}
		errSurf, _ := e.Draw(ctx)
		root.AddChild(0, 9, errSurf)
	}

	return root, nil
}
