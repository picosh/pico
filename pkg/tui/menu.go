package tui

import (
	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/pkg/db"
)

var menuChoices = []string{
	"pubkeys",
	"tokens",
	"logs",
	"analytics",
	"tuns",
	"pico+",
	"chat",
}

type LoadedUsageStats struct{}

type MenuPage struct {
	shared *SharedModel

	list     list.Dynamic
	features *FeaturesList
	stats    *db.UserStats
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

func NewMenuPage(shrd *SharedModel) *MenuPage {
	m := &MenuPage{shared: shrd}
	m.list = list.Dynamic{Builder: getMenuWidget, DrawCursor: true}
	m.features = NewFeaturesList(shrd)
	return m
}

func loadChat(shrd *SharedModel) {
	sp := &SenpaiCmd{
		Shared: shrd,
	}
	_ = sp.Run()
}

func (m *MenuPage) fetchUsageStats() error {
	stats, err := m.shared.Dbpool.FindUserStats(m.shared.User.ID)
	if err != nil {
		return err
	}
	m.stats = stats
	return nil
}

func (m *MenuPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		_ = m.fetchUsageStats()
		cmd, _ := m.features.HandleEvent(vxfw.Init{}, phase)
		return vxfw.BatchCmd([]vxfw.Command{
			cmd,
			vxfw.FocusWidgetCmd(&m.list),
		}), nil
	case vaxis.Key:
		if msg.Matches(vaxis.KeyEnter) {
			choice := menuChoices[m.list.Cursor()]
			m.shared.App.PostEvent(Navigate{To: choice})
		}
	}
	return nil, nil
}

func (m *MenuPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	info, _ := NewUserInfo(m.shared).Draw(ctx)

	brd := NewBorder(&m.list)
	brd.Label = "menu"
	brd.Style = vaxis.Style{Foreground: oj}
	menuSurf, _ := brd.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  30,
			Height: uint16(len(menuChoices)) + 3,
		},
	})

	services, _ := NewServicesList(m.shared.PlusFeatureFlag).Draw(ctx)
	features, _ := m.features.Draw(ctx)

	leftPane := NewGroupStack([]vxfw.Surface{
		menuSurf,
		info,
		services,
	})
	leftSurf, _ := leftPane.Draw(ctx)

	right := []vxfw.Surface{}
	if len(m.features.features) > 0 {
		right = append(right, features)
	}

	if m.stats != nil {
		pages, _ := NewUsageInfo("pages", &m.stats.Pages).Draw(ctx)
		prose, _ := NewUsageInfo("prose", &m.stats.Prose).Draw(ctx)
		pastes, _ := NewUsageInfo("pastes", &m.stats.Pastes).Draw(ctx)
		feeds, _ := NewUsageInfo("rss-to-email", &m.stats.Feeds).Draw(ctx)
		right = append(right,
			pages,
			prose,
			pastes,
			feeds,
		)
	}
	rightPane := NewGroupStack(right)
	rightSurf, _ := rightPane.Draw(ctx)

	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)
	root.AddChild(0, 0, leftSurf)
	root.AddChild(30, 0, rightSurf)
	return root, nil
}
