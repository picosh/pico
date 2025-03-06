package tui

import (
	"fmt"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/db"
	"github.com/picosh/utils"
)

type SitesLoaded struct{}
type SiteStatsLoaded struct{}

type AnalyticsPage struct {
	shared *SharedModel

	sites    []*db.VisitUrl
	list     list.Dynamic
	err      error
	stats    map[string]*db.SummaryVisits
	selected string
	interval string
	focus    int
}

var focusWdgt = []string{
	"sites",
	"urls",
	"not found",
	"referers",
	"visits",
}

func NewAnalyticsPage(shrd *SharedModel) *AnalyticsPage {
	page := &AnalyticsPage{
		shared:   shrd,
		stats:    map[string]*db.SummaryVisits{},
		interval: "month",
	}

	page.list = list.Dynamic{DrawCursor: true, Builder: page.getWidget}
	return page
}

func (m *AnalyticsPage) Footer() []Shortcut {
	return []Shortcut{
		{Shortcut: "j/k", Text: "choose"},
		{Shortcut: "tab", Text: "focus"},
	}
}

func (m *AnalyticsPage) getWidget(i uint, cursor uint) vxfw.Widget {
	if int(i) >= len(m.sites) {
		return nil
	}

	site := m.sites[i]
	txt := text.New(site.Url)
	return txt
}

func (m *AnalyticsPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		go m.fetchSites()
		return vxfw.FocusWidgetCmd(m), nil
	case SitesLoaded:
		return vxfw.BatchCmd([]vxfw.Command{
			vxfw.FocusWidgetCmd(&m.list),
			vxfw.RedrawCmd{},
		}), nil
	case SiteStatsLoaded:
		return vxfw.RedrawCmd{}, nil
	case vaxis.Key:
		if msg.Matches(vaxis.KeyEnter) {
			m.selected = m.sites[m.list.Cursor()].Url
			go m.fetchSiteStats(m.selected, m.interval)
			return vxfw.RedrawCmd{}, nil
		}
		if msg.Matches(vaxis.KeyTab) {
			if m.focus == len(focusWdgt)-1 {
				m.focus = 0
			} else {
				m.focus += 1
			}
			return vxfw.RedrawCmd{}, nil
		}
	}
	return nil, nil
}

func (m *AnalyticsPage) focusBorder(brd *Border) {
	focus := focusWdgt[m.focus]
	fmt.Println(focus, brd.Label)
	if focus == brd.Label {
		fmt.Println("fufufufu")
		brd.Style = vaxis.Style{Foreground: oj}
	} else {
		brd.Style = vaxis.Style{Foreground: purp}
	}
}

func (m *AnalyticsPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)
	leftPaneW := float32(ctx.Max.Width) * 0.35

	var wdgt vxfw.Widget = text.New("No sites found")
	if len(m.sites) > 0 {
		wdgt = &m.list
	}

	leftPane := NewBorder(wdgt)
	leftPane.Label = "sites"
	m.focusBorder(leftPane)
	leftSurf, _ := leftPane.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  uint16(leftPaneW),
			Height: ctx.Max.Height,
		},
	})

	root.AddChild(0, 0, leftSurf)

	rightPaneW := float32(ctx.Max.Width) * 0.65
	if m.selected == "" {
		rightWdgt := text.New("Select a site on the left to view its stats")
		rightSurf, _ := rightWdgt.Draw(vxfw.DrawContext{
			Characters: ctx.Characters,
			Max: vxfw.Size{
				Width:  uint16(rightPaneW),
				Height: ctx.Max.Height,
			},
		})
		root.AddChild(int(leftPaneW), 0, rightSurf)
	} else {
		rightSurf := vxfw.NewSurface(uint16(rightPaneW), ctx.Max.Height, m)
		ah := 0

		data, err := m.getSiteData()
		if err != nil {
			txt, _ := text.New("No data found").Draw(ctx)
			rightSurf.AddChild(0, 0, txt)
			root.AddChild(int(leftPaneW), 0, rightSurf)
			return root, nil
		}

		urlSurf, _ := m.urls(data.TopUrls, "urls").Draw(vxfw.DrawContext{
			Characters: vaxis.Characters,
			Max: vxfw.Size{
				Width:  uint16(rightPaneW),
				Height: uint16(len(data.TopUrls) + 3),
			},
		})
		rightSurf.AddChild(0, ah, urlSurf)
		ah += int(urlSurf.Size.Height)

		urlSurf, _ = m.urls(data.NotFoundUrls, "not found").Draw(vxfw.DrawContext{
			Characters: vaxis.Characters,
			Max: vxfw.Size{
				Width:  uint16(rightPaneW),
				Height: uint16(len(data.NotFoundUrls) + 3),
			},
		})
		rightSurf.AddChild(0, ah, urlSurf)
		ah += int(urlSurf.Size.Height)

		urlSurf, _ = m.urls(data.TopReferers, "referers").Draw(vxfw.DrawContext{
			Characters: vaxis.Characters,
			Max: vxfw.Size{
				Width:  uint16(rightPaneW),
				Height: uint16(len(data.TopReferers) + 3),
			},
		})
		rightSurf.AddChild(0, ah, urlSurf)
		ah += int(urlSurf.Size.Height)

		surf, _ := m.visits(data.Intervals).Draw(vxfw.DrawContext{
			Characters: vaxis.Characters,
			Max: vxfw.Size{
				Width:  uint16(rightPaneW),
				Height: uint16(len(data.Intervals) + 3),
			},
		})
		rightSurf.AddChild(0, ah, surf)

		root.AddChild(int(leftPaneW), 0, rightSurf)
	}

	return root, nil
}

func (m *AnalyticsPage) getSiteData() (*db.SummaryVisits, error) {
	val, ok := m.stats[m.selected+":"+m.interval]
	if !ok {
		return nil, fmt.Errorf("site data not found")
	}
	return val, nil
}

func (m *AnalyticsPage) urls(urls []*db.VisitUrl, label string) vxfw.Widget {
	segs := []vaxis.Segment{}
	for _, url := range urls {
		segs = append(segs, vaxis.Segment{Text: fmt.Sprintf("%s: %d\n", url.Url, url.Count)})
	}
	wdgt := richtext.New(segs)
	rightPane := NewBorder(wdgt)
	rightPane.Label = label
	m.focusBorder(rightPane)
	return rightPane
}

func (m *AnalyticsPage) visits(intervals []*db.VisitInterval) vxfw.Widget {
	segs := []vaxis.Segment{}
	for _, visit := range intervals {
		segs = append(
			segs,
			vaxis.Segment{
				Text: fmt.Sprintf("%s: %d\n", visit.Interval.Format(time.RFC3339), visit.Visitors),
			},
		)
	}
	wdgt := richtext.New(segs)
	rightPane := NewBorder(wdgt)
	rightPane.Label = "visits"
	m.focusBorder(rightPane)
	return rightPane
}

func (m *AnalyticsPage) fetchSites() {
	siteList, err := m.shared.Dbpool.FindVisitSiteList(&db.SummaryOpts{
		UserID: m.shared.User.ID,
		Origin: utils.StartOfMonth(),
	})
	if err != nil {
		m.err = err
	}
	m.sites = siteList
	m.shared.App.PostEvent(SitesLoaded{})
}

func (m *AnalyticsPage) fetchSiteStats(site string, interval string) {
	opts := &db.SummaryOpts{
		Host: site,

		UserID:   m.shared.User.ID,
		Interval: interval,
	}

	if interval == "day" {
		opts.Origin = utils.StartOfMonth()
	} else {
		opts.Origin = utils.StartOfYear()
	}

	summary, err := m.shared.Dbpool.VisitSummary(opts)
	if err != nil {
		m.err = err
		return
	}
	m.stats[site+":"+interval] = summary
	m.shared.App.PostEvent(SiteStatsLoaded{})
}
