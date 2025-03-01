package tuivax

import (
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/tui/common"
)

var menuChoices = []string{
	"pubkeys",
	"tokens",
	"settings",
	"logs",
	"analytics",
	"chat",
	"pico+",
}

type MenuPage struct {
	shared *common.SharedModel

	list list.Dynamic
}

func getMenuWidget(i uint, cursor uint) vxfw.Widget {
	if int(i) >= len(menuChoices) {
		return nil
	}
	var style vaxis.Style
	if i == cursor {
		style.Attribute = vaxis.AttrReverse
	}
	content := menuChoices[i]
	return &text.Text{
		Content: content,
		Style:   style,
	}
}

func NewMenuPage(shrd *common.SharedModel) *MenuPage {
	m := &MenuPage{shared: shrd}
	m.list = list.Dynamic{Builder: getMenuWidget, DrawCursor: true}
	return m
}

func (m *MenuPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vaxis.FocusIn:
		return vxfw.FocusWidgetCmd(&m.list), nil
	case vaxis.Key:
		if msg.Matches(vaxis.KeyEnter) {
			m.shared.App.PostEvent(Navigate{To: menuChoices[m.list.Cursor()]})
		}
	}
	return nil, nil
}

func (m *MenuPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	createdAt := m.shared.User.CreatedAt.Format(time.DateOnly)
	pink := vaxis.Style{Foreground: fuschia}

	segs := []vaxis.Segment{}
	segs = append(
		segs,
		vaxis.Segment{Text: "│", Style: pink},
		vaxis.Segment{Text: " Username: "},
		vaxis.Segment{Text: m.shared.User.Name, Style: pink},
		vaxis.Segment{Text: "\n"},
		vaxis.Segment{Text: "│", Style: pink},
		vaxis.Segment{Text: " Joined: "},
		vaxis.Segment{Text: createdAt, Style: pink},
	)

	brdH := 2
	if m.shared.PlusFeatureFlag != nil {
		expiresAt := m.shared.PlusFeatureFlag.ExpiresAt.Format(time.DateOnly)
		segs = append(segs,
			vaxis.Segment{Text: "\n"},
			vaxis.Segment{Text: "│", Style: pink},
			vaxis.Segment{Text: " Pico+ Expires: "}, vaxis.Segment{Text: expiresAt, Style: pink},
			vaxis.Segment{Text: "\n"},
		)
		brdH += 1
	}

	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)

	infoWdgt := richtext.New(segs)
	infoSurf, _ := infoWdgt.Draw(ctx)
	root.AddChild(0, 0, infoSurf)

	offset := brdH + 1
	listSurf, _ := m.list.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  ctx.Max.Width,
			Height: ctx.Max.Height - uint16(offset),
		},
	})
	root.AddChild(0, offset, listSurf)
	// menuWin := win.New(0, offset, win.Width, win.Height-offset)
	return root, nil
}
