package tuivax

import (
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/button"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
)

type SettingsPage struct {
	shared *common.SharedModel

	focus    string
	err      error
	features []*db.FeatureFlag
	btn      *button.Button
}

func NewSettingsPage(shrd *common.SharedModel) *SettingsPage {
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

func (m *SettingsPage) fetchFeatures() error {
	features, err := m.shared.Dbpool.FindFeaturesForUser(m.shared.User.ID)
	m.features = features
	return err
}

func (m *SettingsPage) toggleAnalytics() error {
	if m.findAnalyticsFeature() == nil {
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
		if msg.Matches(vaxis.KeyTab) {
			fcs := vxfw.FocusWidgetCmd(m)
			if m.focus == "" {
				m.focus = "button"
				fcs = vxfw.FocusWidgetCmd(m.btn)
			} else {
				m.focus = ""
			}
			return vxfw.BatchCmd([]vxfw.Command{
				fcs,
				vxfw.RedrawCmd{},
			}), nil
		}

		if msg.Matches(vaxis.KeyEnter) {
			if m.focus == "button" {
				m.err = m.toggleAnalytics()
				return vxfw.RedrawCmd{}, nil
			}
		}
	}
	return nil, nil
}

func (m *SettingsPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch ev.(type) {
	case PageIn:
		m.err = m.fetchFeatures()
	}
	return nil, nil
}

func (m *SettingsPage) findAnalyticsFeature() *db.FeatureFlag {
	for _, feature := range m.features {
		if feature.Name == "analytics" {
			return feature
		}
	}
	return nil
}

func (m *SettingsPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	root := vxfw.NewSurface(w, h, m)

	hasPlus := m.shared.PlusFeatureFlag != nil
	active := vaxis.Style{Foreground: green}
	inactive := vaxis.Style{Foreground: red}
	segs := []vaxis.Segment{
		{Text: "Name "}, {Text: "Status\n"},
		{Text: "prose "}, {Text: "active\n", Style: active},
		{Text: "pipe "}, {Text: "active\n", Style: active},
		{Text: "pastes "}, {Text: "active\n", Style: active},
		{Text: "rss-to-email "}, {Text: "active\n", Style: active},
	}

	if hasPlus {
		segs = append(
			segs,
			vaxis.Segment{Text: "pages "}, vaxis.Segment{Text: "active\n", Style: active},
			vaxis.Segment{Text: "tuns "}, vaxis.Segment{Text: "active\n", Style: active},
			vaxis.Segment{Text: "irc bouncer "}, vaxis.Segment{Text: "active\n", Style: active},
		)
	} else {
		segs = append(
			segs,
			vaxis.Segment{Text: "pages "}, vaxis.Segment{Text: "free tier\n", Style: active},
			vaxis.Segment{Text: "tuns "}, vaxis.Segment{Text: "requires pico+\n", Style: inactive},
			vaxis.Segment{Text: "irc bouncer "}, vaxis.Segment{Text: "requires pico+", Style: inactive},
		)
	}

	yPos := 0
	txt := richtext.New(segs)
	txt.Softwrap = false
	txtSurf, _ := txt.Draw(ctx)
	root.AddChild(0, yPos, txtSurf)
	yPos += int(txtSurf.Size.Height)

	features := []vaxis.Segment{
		{Text: "Name "}, {Text: "Expires At\n"},
	}
	for _, feature := range m.features {
		features = append(
			features,
			vaxis.Segment{Text: feature.Name + " "},
			vaxis.Segment{Text: feature.ExpiresAt.Format(time.DateOnly) + "\n"},
		)
	}

	if len(m.features) > 0 {
		ft, _ := richtext.New(features).Draw(ctx)
		root.AddChild(0, yPos+1, ft)
		yPos += int(ft.Size.Height) + 1
	} else {
		yPos += 1
	}

	analytics := text.New(`Get usage statistics on your blog, blog posts, and pages sites. For example, see unique visitors, most popular URLs, and top referers.

We do not collect usage statistic unless analytics is enabled. Further, when analytics are disabled we do not purge usage statistics.

We will only store usage statistics for 1 year from when the event was created.`)
	analyticsSurf, _ := analytics.Draw(ctx)
	root.AddChild(0, yPos, analyticsSurf)
	yPos += int(analyticsSurf.Size.Height)

	str := "Analytics is only available to pico+ users."
	style := vaxis.Style{Foreground: red}
	if hasPlus {
		style = vaxis.Style{Foreground: white}
		ff := m.findAnalyticsFeature()
		if ff == nil {
			str = "Enable analytics"
		} else {
			str = "Disable analytics"
		}
	}
	t := text.New(str)
	t.Style = style
	a, _ := t.Draw(ctx)
	root.AddChild(0, yPos+1, a)

	if hasPlus {
		btnSurf, _ := m.btn.Draw(vxfw.DrawContext{
			Characters: ctx.Characters,
			Max:        vxfw.Size{Width: 10, Height: 1},
		})
		root.AddChild(20, yPos+1, btnSurf)
	}

	return root, nil
}
