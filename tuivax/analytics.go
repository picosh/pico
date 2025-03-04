package tuivax

import (
	"fmt"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
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
	interval string
}

func NewAnalyticsPage(shrd *common.SharedModel) *AnalyticsPage {
	page := &AnalyticsPage{
		shared:   shrd,
		stats:    map[string]*db.SummaryVisits{},
		interval: "month",
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
	case SiteStatsLoaded:
		return vxfw.RedrawCmd{}, nil
	case vaxis.Key:
		if msg.Matches(vaxis.KeyEnter) {
			m.selected = m.sites[m.list.Cursor()].Url
			go m.fetchSiteStats(m.selected, m.interval)
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
		ah := 0
		urlSurf, _ := m.topUrls().Draw(ctx)
		rightPane.AddChild(0, ah, urlSurf)
		ah += int(urlSurf.Size.Height)

		refSurf, _ := m.topRefs().Draw(ctx)
		rightPane.AddChild(0, ah, refSurf)
		ah += int(refSurf.Size.Height)

		notFoundSurf, _ := m.topNotFound().Draw(ctx)
		rightPane.AddChild(0, ah, notFoundSurf)
		ah += int(notFoundSurf.Size.Height)

		visitsSurf, _ := m.visits().Draw(ctx)
		rightPane.AddChild(0, ah, visitsSurf)
		ah += int(visitsSurf.Size.Height)
	}
	root.AddChild(int(leftPaneW), 0, rightPane)

	return root, nil
}

func (m *AnalyticsPage) getSiteData() (*db.SummaryVisits, error) {
	val, ok := m.stats[m.selected+":"+m.interval]
	if !ok {
		return nil, fmt.Errorf("site data not found")
	}
	return val, nil
}

func (m *AnalyticsPage) topUrls() vxfw.Widget {
	data, err := m.getSiteData()
	if err != nil {
		return text.New("No urls found")
	}
	segs := []vaxis.Segment{}
	for _, url := range data.TopUrls {
		segs = append(segs, vaxis.Segment{Text: fmt.Sprintf("%s: %d\n", url.Url, url.Count)})
	}
	return richtext.New(segs)
}

func (m *AnalyticsPage) topRefs() vxfw.Widget {
	data, err := m.getSiteData()
	if err != nil {
		return text.New("No refs found")
	}
	segs := []vaxis.Segment{}
	for _, url := range data.TopReferers {
		segs = append(segs, vaxis.Segment{Text: fmt.Sprintf("%s: %d\n", url.Url, url.Count)})
	}
	return richtext.New(segs)
}

func (m *AnalyticsPage) topNotFound() vxfw.Widget {
	data, err := m.getSiteData()
	if err != nil {
		return text.New("No not found urls")
	}
	segs := []vaxis.Segment{}
	for _, url := range data.NotFoundUrls {
		segs = append(segs, vaxis.Segment{Text: fmt.Sprintf("%s: %d\n", url.Url, url.Count)})
	}
	return richtext.New(segs)
}

func (m *AnalyticsPage) visits() vxfw.Widget {
	data, err := m.getSiteData()
	if err != nil {
		return text.New("No visits found")
	}
	segs := []vaxis.Segment{}
	for _, visit := range data.Intervals {
		segs = append(
			segs,
			vaxis.Segment{
				Text: fmt.Sprintf("%s: %d\n", visit.Interval.Format(time.RFC3339), visit.Visitors),
			},
		)
	}
	return richtext.New(segs)
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
