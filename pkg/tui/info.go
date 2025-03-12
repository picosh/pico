package tui

import (
	"fmt"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/pkg/db"
)

type UsageInfo struct {
	stats *db.UserServiceStats
	Label string
}

func NewUsageInfo(label string, stats *db.UserServiceStats) *UsageInfo {
	return &UsageInfo{Label: label, stats: stats}
}

func (m *UsageInfo) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (m *UsageInfo) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	info := NewKv(m.getKv)
	brd := NewBorder(info)
	brd.Label = m.Label
	brd.Style = vaxis.Style{Foreground: purp}
	return brd.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  30,
			Height: 3 + 3,
		},
	})
}

func (m *UsageInfo) getKv(idx uint16) (vxfw.Widget, vxfw.Widget) {
	if int(idx) >= 3 {
		return nil, nil
	}
	label := "posts"
	if m.Label == "pages" {
		label = "sites"
	}
	kv := [][]string{
		{label, fmt.Sprintf("%d", m.stats.Num)},
		{"oldest", m.stats.FirstCreatedAt.Format(time.DateOnly)},
		{"newest", m.stats.LastestCreatedAt.Format(time.DateOnly)},
	}
	return text.New(kv[idx][0]), text.New(kv[idx][1])
}

type UserInfo struct {
	shared *SharedModel
}

func NewUserInfo(shrd *SharedModel) *UserInfo {
	return &UserInfo{shrd}
}

func (m *UserInfo) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (m *UserInfo) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	features := NewKv(m.getKv)
	brd := NewBorder(features)
	brd.Label = "info"
	brd.Style = vaxis.Style{Foreground: purp}
	h := 1
	if m.shared.PlusFeatureFlag != nil {
		h += 1
	}
	return brd.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  30,
			Height: uint16(h) + 3,
		},
	})
}

func (m *UserInfo) getKv(idx uint16) (vxfw.Widget, vxfw.Widget) {
	if int(idx) >= 2 {
		return nil, nil
	}

	createdAt := m.shared.User.CreatedAt.Format(time.DateOnly)
	if idx == 0 {
		return text.New("joined"), text.New(createdAt)
	}

	if m.shared.PlusFeatureFlag != nil {
		expiresAt := m.shared.PlusFeatureFlag.ExpiresAt.Format(time.DateOnly)
		return text.New("pico+ expires"), text.New(expiresAt)
	}

	return nil, nil
}

type FeaturesList struct {
	shared   *SharedModel
	features []*db.FeatureFlag
	err      error
}

func NewFeaturesList(shrd *SharedModel) *FeaturesList {
	return &FeaturesList{shared: shrd}
}

func (m *FeaturesList) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch ev.(type) {
	case vxfw.Init:
		m.err = m.fetchFeatures()
		return vxfw.RedrawCmd{}, nil
	}
	return nil, nil
}

func (m *FeaturesList) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	features := NewKv(m.getFeaturesKv)
	brd := NewBorder(features)
	brd.Label = "features"
	brd.Style = vaxis.Style{Foreground: purp}
	return brd.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  30,
			Height: uint16(len(m.features)) + 4,
		},
	})
}

func (m *FeaturesList) fetchFeatures() error {
	features, err := m.shared.Dbpool.FindFeaturesForUser(m.shared.User.ID)
	m.features = features
	return err
}

func (m *FeaturesList) getFeaturesKv(idx uint16) (vxfw.Widget, vxfw.Widget) {
	kv := [][]string{
		{"name", "expires at"},
	}
	for _, feature := range m.features {
		kv = append(kv, []string{feature.Name, feature.ExpiresAt.Format(time.DateOnly)})
	}

	if int(idx) >= len(kv) {
		return nil, nil
	}

	key := text.New(kv[idx][0])
	value := text.New(kv[idx][1])

	if idx == 0 {
		style := vaxis.Style{UnderlineColor: purp, UnderlineStyle: vaxis.UnderlineDashed, Foreground: purp}
		key.Style = style
		value.Style = style
	}

	return key, value
}

type ServicesList struct {
	ff *db.FeatureFlag
}

func NewServicesList(ff *db.FeatureFlag) *ServicesList {
	return &ServicesList{ff}
}

func (m *ServicesList) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (m *ServicesList) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	services := NewKv(m.getServiceKv)
	brd := NewBorder(services)
	brd.Label = "services"
	brd.Style = vaxis.Style{Foreground: purp}
	servicesHeight := 8
	return brd.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  30,
			Height: uint16(servicesHeight) + 3,
		},
	})
}

func (m *ServicesList) getServiceKv(idx uint16) (vxfw.Widget, vxfw.Widget) {
	hasPlus := m.ff != nil
	kv := [][]string{
		{"name", "status"},
		{"prose", "active"},
		{"pipe", "active"},
		{"pastes", "active"},
		{"rss-to-email", "active"},
	}

	if hasPlus {
		kv = append(
			kv,
			[]string{"pages", "active"},
			[]string{"tuns", "active"},
			[]string{"irc bouncer", "active"},
		)
	} else {
		kv = append(
			kv,
			[]string{"pages", "free tier"},
			[]string{"tuns", "pico+"},
			[]string{"irc bouncer", "pico+"},
		)
	}

	if int(idx) >= len(kv) {
		return nil, nil
	}

	key := text.New(kv[idx][0])
	value := text.New(kv[idx][1])
	val := kv[idx][1]

	if val == "active" {
		value.Style = vaxis.Style{Foreground: green}
	} else if val == "free tier" {
		value.Style = vaxis.Style{Foreground: oj}
	} else {
		value.Style = vaxis.Style{Foreground: red}
	}

	if idx == 0 {
		style := vaxis.Style{UnderlineColor: purp, UnderlineStyle: vaxis.UnderlineDashed, Foreground: purp}
		key.Style = style
		value.Style = style
	}

	return key, value
}
