package tuivax

import (
	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/utils"
)

type SitesLoaded struct{}
type SiteStatsLoaded struct{}

type AnalyticsPage struct {
	shared *common.SharedModel

	sites    []*db.VisitUrl
	list     list.Dynamic
	err      error
	stats    map[string]*db.SummaryVisits
	selected string
}

func NewAnalyticsPage(shrd *common.SharedModel) *AnalyticsPage {
	page := &AnalyticsPage{
		shared: shrd,
	}

	page.list = list.Dynamic{DrawCursor: true, Builder: page.getWidget}
	return page
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
	case vaxis.Key:
		if msg.Matches(vaxis.KeyEnter) {
			m.selected = m.sites[m.list.Cursor()].Url
			return vxfw.RedrawCmd{}, nil
		}
	}
	return nil, nil
}

func (m *AnalyticsPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)
	leftPaneW := float32(ctx.Max.Width) * 0.35
	leftPane := vxfw.NewSurface(uint16(leftPaneW), ctx.Max.Height, m)

	if len(m.sites) == 0 {
		txt := text.New("No sites found")
		surf, _ := txt.Draw(ctx)
		leftPane.AddChild(0, 0, surf)
	} else {
		leftSurf, _ := m.list.Draw(ctx)
		leftPane.AddChild(0, 0, leftSurf)
	}
	root.AddChild(0, 0, leftPane)

	rightPaneW := float32(ctx.Max.Width) * 0.65
	rightPane := vxfw.NewSurface(uint16(rightPaneW), ctx.Max.Height, m)

	if m.selected == "" {
		rightTxt := text.New("Select a site on the left to view its stats")
		rightSurf, _ := rightTxt.Draw(ctx)
		rightPane.AddChild(0, 0, rightSurf)
	} else {
		surf, _ := m.topUrls()
		rightPane.AddChild(0, 0, surf)
	}
	root.AddChild(int(leftPaneW), 0, rightPane)

	return root, nil
}

func (m *AnalyticsPage) topUrls() (vxfw.Surface, error) {
	return vxfw.Surface{}, nil
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
