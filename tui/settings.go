package tui

import (
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/button"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/db"
)

type SettingsPage struct {
	shared *SharedModel

	focus    string
	err      error
	features []*db.FeatureFlag
	btn      *button.Button
}

func NewSettingsPage(shrd *SharedModel) *SettingsPage {
	btn := button.New("OK", func() (vxfw.Command, error) { return nil, nil })
	btn.Style = button.StyleSet{
		Default: vaxis.Style{Background: grey},
		Hover:   vaxis.Style{Background: fuschia},
		Focus:   vaxis.Style{Background: fuschia},
	}
	return &SettingsPage{
		shared: shrd,
		btn:    btn,
	}
}

func (m *SettingsPage) Footer() []Shortcut {
	return []Shortcut{
		{Shortcut: "enter", Text: "toggle analytics"},
	}
}

func (m *SettingsPage) fetchFeatures() error {
	features, err := m.shared.Dbpool.FindFeaturesForUser(m.shared.User.ID)
	m.features = features
	return err
}

func (m *SettingsPage) toggleAnalytics() error {
	if findAnalyticsFeature(m.features) == nil {
		now := time.Now()
		expiresAt := now.AddDate(100, 0, 0)
		_, err := m.shared.Dbpool.InsertFeature(m.shared.User.ID, "analytics", expiresAt)
		if err != nil {
			return err
		}
	} else {
		err := m.shared.Dbpool.RemoveFeature(m.shared.User.ID, "analytics")
		if err != nil {
			return err
		}
	}

	return m.fetchFeatures()
}

func (m *SettingsPage) CaptureEvent(ev vaxis.Event) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vaxis.Key:
		if msg.Matches(vaxis.KeyEnter) {
			m.err = m.toggleAnalytics()
			return vxfw.RedrawCmd{}, nil
		}
	}
	return nil, nil
}

func (m *SettingsPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch ev.(type) {
	case PageIn:
		m.err = m.fetchFeatures()
		return vxfw.FocusWidgetCmd(m), nil
	}
	return nil, nil
}

func (m *SettingsPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	root := vxfw.NewSurface(w, h, m)

	hasPlus := m.shared.PlusFeatureFlag != nil
	// active := vaxis.Style{Foreground: green}
	// inactive := vaxis.Style{Foreground: red}
	kv := map[string]string{
		"Name":         "Status",
		"prose":        "active",
		"pipe":         "active",
		"pastes":       "active",
		"rss-to-email": "active",
		"pages":        "free tier",
		"tuns":         "requires pico+",
		"irc bouncer":  "requires pico+",
	}

	if hasPlus {
		kv["pages"] = "active"
		kv["tuns"] = "active"
		kv["irc bouncer"] = "active"
	}

	yPos := 0
	services := NewKv(kv)
	brd := NewBorder(services)
	brd.Label = "services"
	brd.Style = vaxis.Style{Foreground: purp}
	servicesSurf, _ := brd.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  30,
			Height: uint16(len(kv)) + 3,
		},
	})
	root.AddChild(0, yPos, servicesSurf)
	yPos += len(kv) + 3

	kv = map[string]string{
		"Name": "Expires At",
	}
	for _, feature := range m.features {
		kv[feature.Name] = feature.ExpiresAt.Format(time.DateOnly)
	}

	features := NewKv(kv)
	if len(m.features) > 0 {
		brd := NewBorder(features)
		brd.Label = "features"
		brd.Style = vaxis.Style{Foreground: purp}
		featSurf, _ := brd.Draw(vxfw.DrawContext{
			Characters: ctx.Characters,
			Max: vxfw.Size{
				Width:  30,
				Height: uint16(len(kv)) + 3,
			},
		})
		root.AddChild(0, yPos, featSurf)
		yPos += len(kv) + 3
	} else {
		yPos += 1
	}

	analytics := text.New(`Get usage statistics on your blog, blog posts, and pages sites. For example, see unique visitors, most popular URLs, and top referers.

We do not collect usage statistic unless analytics is enabled. Further, when analytics are disabled we do not purge usage statistics.

We will only store usage statistics for 1 year from when the event was created.`)

	str := "Analytics is only available to pico+ users."
	style := vaxis.Style{Foreground: red}
	if hasPlus {
		style = vaxis.Style{Foreground: fuschia}
		ff := findAnalyticsFeature(m.features)
		if ff == nil {
			str = "Enable analytics"
		} else {
			str = "Disable analytics"
		}
	}
	t := text.New(str)
	t.Style = style
	tt, _ := t.Draw(ctx)

	stack := []vxfw.Surface{}
	ana, _ := analytics.Draw(ctx)
	stack = append(stack, ana, tt)

	if hasPlus {
		btnSurf, _ := m.btn.Draw(vxfw.DrawContext{
			Characters: ctx.Characters,
			Max:        vxfw.Size{Width: 10, Height: 1},
		})
		stack = append(stack, btnSurf)
	}

	gstack := NewGroupStack(stack)
	gstack.Gap = 1
	brd = NewBorder(gstack)
	brd.Label = "analytics"
	brd.Style = vaxis.Style{Foreground: purp}
	anaSurf, _ := brd.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  40,
			Height: ctx.Max.Height,
		},
	})
	root.AddChild(0, yPos, anaSurf)

	return root, nil
}

func findAnalyticsFeature(features []*db.FeatureFlag) *db.FeatureFlag {
	for _, feature := range features {
		if feature.Name == "analytics" {
			return feature
		}
	}
	return nil
}
