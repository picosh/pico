package tuivax

import (
	"fmt"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/button"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"git.sr.ht/~rockorager/vaxis/vxfw/textfield"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
)

type TokensPage struct {
	shared *common.SharedModel
	list   list.Dynamic

	tokens  []*db.Token
	err     error
	confirm bool
}

func NewTokensPage(shrd *common.SharedModel) *TokensPage {
	m := &TokensPage{
		shared: shrd,
	}
	m.list = list.Dynamic{DrawCursor: true, Builder: m.getWidget}
	return m
}

type FetchTokens struct{}

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
	txt.Softwrap = false

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

	segs := []vaxis.Segment{
		{
			Text:  "j/k, ↑/↓: choose, x: delete, c: create, esc: exit\n",
			Style: vaxis.Style{Foreground: grey},
		},
	}
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
	shared *common.SharedModel

	token string
	err   error
	focus string
	input *textfield.TextField
	btn   *button.Button
}

func NewAddTokenPage(shrd *common.SharedModel) *AddTokenPage {
	btn := button.New("OK", func() (vxfw.Command, error) { return nil, nil })
	btn.Style = button.StyleSet{
		Default: vaxis.Style{Background: grey},
		Focus:   vaxis.Style{Background: fuschia},
	}
	return &AddTokenPage{
		shared: shrd,

		input: textfield.New(),
		btn:   btn,
	}
}

func (m *AddTokenPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		m.focus = "input"
		m.input.Reset()
		return vxfw.FocusWidgetCmd(m.input), nil
	case vaxis.Key:
		if msg.Matches(vaxis.KeyTab) {
			if m.token != "" {
				return nil, nil
			}
			fcs := vxfw.FocusWidgetCmd(m.input)
			if m.focus == "input" {
				m.focus = "button"
				fcs = vxfw.FocusWidgetCmd(m.btn)
			} else {
				m.focus = "input"
			}
			return vxfw.BatchCmd([]vxfw.Command{
				fcs,
				vxfw.RedrawCmd{},
			}), nil
		}
		if msg.Matches(vaxis.KeyEnter) {
			if m.focus == "button" {
				if m.token != "" {
					m.token = ""
					m.err = nil
					m.input.Value = ""
					m.shared.App.PostEvent(Navigate{To: "tokens"})
					return vxfw.RedrawCmd{}, nil
				}
				token, err := m.addToken(m.input.Value)
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

		inputSurf, _ := m.input.Draw(ctx)
		root.AddChild(0, 3, inputSurf)

		btnSurf, _ := m.btn.Draw(vxfw.DrawContext{
			Characters: ctx.Characters,
			Max:        vxfw.Size{Width: 5, Height: 1},
		})
		root.AddChild(0, 5, btnSurf)
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
