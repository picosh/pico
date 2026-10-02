package tui

import (
	"fmt"
	"time"

	"github.com/picosh/pico/pkg/db"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/vxfw"
)

type UsageInfo struct {
	stats *db.UserServiceStats
	Label string
}

func NewUsageInfo(label string, stats *db.UserServiceStats) *UsageInfo {
	return &UsageInfo{Label: label, stats: stats}
}

func (m *UsageInfo) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	info := NewKv(m.getKv())
	brd := NewBorder(info)
	brd.Label = m.Label
	brd.Style = vaxis.Style{Foreground: purp}
	return brd.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  30,
			Height: 4 + 3,
		},
	})
}

func (m *UsageInfo) getKv() []Kv {
	label := "posts"
	if m.Label == "pages" {
		label = "sites"
	}
	kv := []Kv{
		{Key: label, Value: fmt.Sprintf("%d", m.stats.Num)},
		{Key: "oldest", Value: m.stats.FirstCreatedAt.Format(time.DateOnly)},
		{Key: "newest", Value: m.stats.LastestCreatedAt.Format(time.DateOnly)},
	}
	return kv
}

type UserInfo struct {
	shared *SharedModel
}

func NewUserInfo(shrd *SharedModel) *UserInfo {
	return &UserInfo{shrd}
}

func (m *UserInfo) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	features := NewKv(m.getKv())
	brd := NewBorder(features)
	brd.Label = "info"
	brd.Style = vaxis.Style{Foreground: purp}
	h := 1
	if m.shared.PlusFeatureFlag != nil {
		h += 1
	}
	if m.shared.InvitedBy != "" {
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

func (m *UserInfo) getKv() []Kv {
	createdAt := m.shared.User.CreatedAt.Format(time.DateOnly)
	kv := []Kv{
		{Key: "joined", Value: createdAt},
	}

	if m.shared.PlusFeatureFlag != nil {
		expiresAt := m.shared.PlusFeatureFlag.ExpiresAt.Format(time.DateOnly)
		kv = append(kv, Kv{Key: "pico+ expires", Value: expiresAt})
	}

	if m.shared.InvitedBy != "" {
		kv = append(kv, Kv{Key: "invited by", Value: m.shared.InvitedBy})
	}

	return kv
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
	features := NewKv(m.getFeaturesKv())
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
	features, err := m.shared.Dbpool.FindFeaturesByUser(m.shared.User.ID)
	m.features = features
	return err
}

func (m *FeaturesList) getFeaturesKv() []Kv {
	kv := []Kv{
		{
			Key:   "name",
			Value: "expires at",
			Style: vaxis.Style{
				UnderlineColor: purp,
				UnderlineStyle: vaxis.UnderlineDashed,
				Foreground:     purp,
			},
		},
	}

	for _, feature := range m.features {
		kv = append(kv, Kv{Key: feature.Name, Value: feature.ExpiresAt.Format(time.DateOnly)})
	}

	return kv
}

type ServicesList struct {
	plusFf  *db.FeatureFlag
	pgsFf   *db.FeatureFlag
	proseFf *db.FeatureFlag
}

func NewServicesList(plusFf *db.FeatureFlag, pgsFf *db.FeatureFlag, proseFf *db.FeatureFlag) *ServicesList {
	return &ServicesList{plusFf: plusFf, pgsFf: pgsFf, proseFf: proseFf}
}

func (m *ServicesList) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	services := NewKv(m.getServiceKv())
	brd := NewBorder(services)
	brd.Label = "services"
	brd.Style = vaxis.Style{Foreground: purp}
	servicesHeight := 7
	return brd.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  32,
			Height: uint16(servicesHeight) + 3,
		},
	})
}

func (m *ServicesList) getServiceKv() []Kv {
	hasPlus := m.plusFf != nil && m.plusFf.IsValid()
	hasPgs := m.pgsFf != nil && m.pgsFf.IsValid()
	hasProse := m.proseFf != nil && m.proseFf.IsValid()
	pagesStatus := "invite/pico+"
	if hasPlus || hasPgs {
		pagesStatus = "active"
	}
	proseStatus := "invite/pico+"
	if hasPlus || hasProse {
		proseStatus = "active"
	}
	data := [][]string{
		{"name", "status"},
		{"pipe", "active"},
		{"prose", proseStatus},
		{"pages", pagesStatus},
	}

	if hasPlus {
		data = append(
			data,
			[]string{"tuns", "active"},
			[]string{"irc bouncer", "active"},
			[]string{"rss-to-email", "active"},
		)
	} else {
		data = append(
			data,
			[]string{"tuns", "pico+"},
			[]string{"irc bouncer", "pico+"},
			[]string{"rss-to-email", "pico+"},
		)
	}

	kv := []Kv{}
	for idx, d := range data {
		value := d[1]
		var style vaxis.Style
		if idx == 0 {
			style = vaxis.Style{
				UnderlineColor: purp,
				UnderlineStyle: vaxis.UnderlineDashed,
				Foreground:     purp,
			}
		} else if value == "active" {
			style = vaxis.Style{Foreground: green}
		} else if value == "free tier" {
			style = vaxis.Style{Foreground: oj}
		} else {
			style = vaxis.Style{Foreground: red}
		}

		kv = append(kv, Kv{
			Key:   d[0],
			Value: value,
			Style: style,
		})

	}

	return kv
}
