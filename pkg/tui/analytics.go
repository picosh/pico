package tui

import (
	"fmt"
	"math"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/utils"
)

type SitesLoaded struct{}
type SiteStatsLoaded struct{}

type AnalyticsPage struct {
	shared *SharedModel

	loadingSites   bool
	loadingDetails bool
	sites          []*db.VisitUrl
	features       []*db.FeatureFlag
	err            error
	stats          map[string]*db.SummaryVisits
	selected       string
	interval       string
	focus          string
	leftPane       list.Dynamic
	rightPane      *Pager
}

func NewAnalyticsPage(shrd *SharedModel) *AnalyticsPage {
	page := &AnalyticsPage{
		shared:   shrd,
		stats:    map[string]*db.SummaryVisits{},
		interval: "month",
		focus:    "sites",
	}

	page.leftPane = list.Dynamic{DrawCursor: true, Builder: page.getLeftWidget}
	page.rightPane = NewPager()
	return page
}

func (m *AnalyticsPage) Footer() []Shortcut {
	ff := findAnalyticsFeature(m.features)
	toggle := "enable analytics"
	if ff != nil && ff.IsValid() {
		toggle = "disable analytics"
	}
	short := []Shortcut{
		{Shortcut: "j/k", Text: "choose"},
		{Shortcut: "tab", Text: "focus"},
		{Shortcut: "f", Text: "toggle filter (month/day)"},
	}
	if m.shared.PlusFeatureFlag != nil {
		short = append(short, Shortcut{Shortcut: "t", Text: toggle})
	}
	return short
}

func (m *AnalyticsPage) getLeftWidget(i uint, cursor uint) vxfw.Widget {
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
		m.loadingSites = true
		go m.fetchSites()
		_ = m.fetchFeatures()
		m.focus = "page"
		return vxfw.FocusWidgetCmd(m), nil
	case SitesLoaded:
		m.focus = "sites"
		return vxfw.BatchCmd([]vxfw.Command{
			vxfw.FocusWidgetCmd(&m.leftPane),
			vxfw.RedrawCmd{},
		}), nil
	case SiteStatsLoaded:
		return vxfw.RedrawCmd{}, nil
	case vaxis.Key:
		if msg.Matches('f') {
			if m.interval == "day" {
				m.interval = "month"
			} else {
				m.interval = "day"
			}
			m.loadingDetails = true
			go m.fetchSiteStats(m.selected, m.interval)
			return vxfw.RedrawCmd{}, nil
		}
		if msg.Matches('t') {
			enabled, err := m.toggleAnalytics()
			if err != nil {
				fmt.Println(err)
			}
			var wdgt vxfw.Widget = m
			if enabled {
				m.focus = "sites"
				wdgt = &m.leftPane
			} else {
				m.focus = "page"
			}
			return vxfw.BatchCmd([]vxfw.Command{
				vxfw.FocusWidgetCmd(wdgt),
				vxfw.RedrawCmd{},
			}), nil
		}
		if msg.Matches(vaxis.KeyEnter) {
			m.selected = m.sites[m.leftPane.Cursor()].Url
			m.loadingDetails = true
			go m.fetchSiteStats(m.selected, m.interval)
			return vxfw.RedrawCmd{}, nil
		}
		if msg.Matches(vaxis.KeyTab) {
			var cmd vxfw.Widget
			if m.focus == "sites" && m.selected != "" {
				m.focus = "details"
				cmd = m.rightPane
			} else if m.focus == "details" {
				m.focus = "sites"
				cmd = &m.leftPane
			} else if m.focus == "page" {
				m.focus = "sites"
				cmd = &m.leftPane
			}
			return vxfw.BatchCmd([]vxfw.Command{
				vxfw.FocusWidgetCmd(cmd),
				vxfw.RedrawCmd{},
			}), nil
		}
	}
	return nil, nil
}

func (m *AnalyticsPage) focusBorder(brd *Border) {
	focus := m.focus
	if focus == brd.Label {
		brd.Style = vaxis.Style{Foreground: oj}
	} else {
		brd.Style = vaxis.Style{Foreground: purp}
	}
}

func (m *AnalyticsPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)
	ff := findAnalyticsFeature(m.features)
	if ff == nil || !ff.IsValid() {
		surf := m.banner(ctx)
		root.AddChild(0, 0, surf)
		return root, nil
	}

	leftPaneW := float32(ctx.Max.Width) * 0.35

	var wdgt vxfw.Widget = text.New("No sites found")
	if len(m.sites) > 0 {
		wdgt = &m.leftPane
	}

	if m.loadingSites {
		wdgt = text.New("Loading ...")
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
		rightSurf := vxfw.NewSurface(uint16(rightPaneW), math.MaxUint16, m)

		ah := 0

		data, err := m.getSiteData()
		if err != nil {
			var txt vxfw.Surface
			if m.loadingDetails {
				txt, _ = text.New("Loading ...").Draw(ctx)
			} else {
				txt, _ = text.New("No data found").Draw(ctx)
			}
			m.rightPane.Surface = txt
			rightPane := NewBorder(m.rightPane)
			rightPane.Label = "details"
			m.focusBorder(rightPane)
			pagerSurf, _ := rightPane.Draw(vxfw.DrawContext{
				Characters: ctx.Characters,
				Max:        vxfw.Size{Width: uint16(rightPaneW), Height: ctx.Max.Height},
			})
			rightSurf.AddChild(0, 0, pagerSurf)
			root.AddChild(int(leftPaneW), 0, rightSurf)
			return root, nil
		}

		rightCtx := vxfw.DrawContext{
			Characters: vaxis.Characters,
			Max: vxfw.Size{
				Width:  uint16(rightPaneW) - 2,
				Height: ctx.Max.Height,
			},
		}

		detailSurf, _ := m.detail(rightCtx, data.Intervals).Draw(rightCtx)
		rightSurf.AddChild(0, ah, detailSurf)
		ah += int(detailSurf.Size.Height)

		urlSurf, _ := m.urls(rightCtx, data.TopUrls, "urls").Draw(rightCtx)
		rightSurf.AddChild(0, ah, urlSurf)
		ah += int(urlSurf.Size.Height)

		urlSurf, _ = m.urls(rightCtx, data.NotFoundUrls, "not found").Draw(rightCtx)
		rightSurf.AddChild(0, ah, urlSurf)
		ah += int(urlSurf.Size.Height)

		urlSurf, _ = m.urls(rightCtx, data.TopReferers, "referers").Draw(rightCtx)
		rightSurf.AddChild(0, ah, urlSurf)
		ah += int(urlSurf.Size.Height)

		surf, _ := m.visits(rightCtx, data.Intervals).Draw(rightCtx)
		rightSurf.AddChild(0, ah, surf)

		m.rightPane.Surface = rightSurf
		rightPane := NewBorder(m.rightPane)
		rightPane.Label = "details"
		m.focusBorder(rightPane)
		pagerSurf, _ := rightPane.Draw(rightCtx)

		root.AddChild(int(leftPaneW), 0, pagerSurf)
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

func (m *AnalyticsPage) detail(ctx vxfw.DrawContext, visits []*db.VisitInterval) vxfw.Widget {
	datestr := ""
	now := time.Now()
	if m.interval == "day" {
		datestr += now.Format("2006 Jan") + " by day"
	} else {
		datestr += now.Format("2006") + " by month"
	}
	kv := []Kv{
		{Key: "date range", Value: datestr, Style: vaxis.Style{Foreground: green}},
	}
	sum := 0
	for _, data := range visits {
		sum += data.Visitors
	}
	avg := 0
	if len(visits) > 0 {
		avg = sum / len(visits)
	}

	kv = append(kv, Kv{Key: "avg req/period", Value: fmt.Sprintf("%d", avg)})

	rightPane := NewBorder(NewKv(kv))
	rightPane.Width = ctx.Max.Width
	rightPane.Label = m.selected
	m.focusBorder(rightPane)
	return rightPane
}

func (m *AnalyticsPage) urls(ctx vxfw.DrawContext, urls []*db.VisitUrl, label string) vxfw.Widget {
	kv := []Kv{}
	w := 15
	for _, url := range urls {
		if len(url.Url) > w {
			w = len(url.Url)
		}
		kv = append(kv, Kv{Key: url.Url, Value: fmt.Sprintf("%d", url.Count)})
	}
	wdgt := NewKv(kv)
	wdgt.KeyColWidth = w + 1
	rightPane := NewBorder(wdgt)
	rightPane.Width = ctx.Max.Width
	rightPane.Label = label
	m.focusBorder(rightPane)
	return rightPane
}

func (m *AnalyticsPage) visits(ctx vxfw.DrawContext, intervals []*db.VisitInterval) vxfw.Widget {
	kv := []Kv{}
	w := 0
	for _, visit := range intervals {
		key := visit.Interval.Format(time.DateOnly)
		if len(key) > w {
			w = len(key)
		}
		kv = append(
			kv,
			Kv{
				Key:   key,
				Value: fmt.Sprintf("%d", visit.Visitors),
			},
		)
	}
	wdgt := NewKv(kv)
	wdgt.KeyColWidth = w + 1
	rightPane := NewBorder(wdgt)
	rightPane.Width = ctx.Max.Width
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
		m.loadingSites = false
		m.err = err
		return
	}
	m.sites = siteList
	m.loadingSites = false
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
		m.loadingDetails = false
		return
	}
	m.stats[site+":"+interval] = summary
	m.loadingDetails = false
	m.shared.App.PostEvent(SiteStatsLoaded{})
}

func (m *AnalyticsPage) fetchFeatures() error {
	features, err := m.shared.Dbpool.FindFeaturesForUser(m.shared.User.ID)
	m.features = features
	return err
}

func (m *AnalyticsPage) banner(ctx vxfw.DrawContext) vxfw.Surface {
	style := vaxis.Style{Foreground: red}
	analytics := richtext.New([]vaxis.Segment{
		{
			Text:  "Analytics is only available to pico+ users.\n\n",
			Style: style,
		},
		{
			Text: "Get usage statistics on your blog, blog posts, and pages sites. For example, see unique visitors, most popular URLs, and top referers.\n\n",
		},
		{
			Text: "We do not collect usage statistic unless analytics is enabled. Further, when analytics are disabled we do not purge usage statistics.\n\n",
		},
		{
			Text: "We will only store usage statistics for 1 year from when the event was created.",
		},
	})
	brd := NewBorder(analytics)
	brd.Label = "alert"
	surf, _ := brd.Draw(ctx)
	return surf
}

func (m *AnalyticsPage) toggleAnalytics() (bool, error) {
	enabled := false
	if findAnalyticsFeature(m.features) == nil {
		now := time.Now()
		expiresAt := now.AddDate(100, 0, 0)
		_, err := m.shared.Dbpool.InsertFeature(m.shared.User.ID, "analytics", expiresAt)
		if err != nil {
			return false, err
		}
		enabled = true
	} else {
		err := m.shared.Dbpool.RemoveFeature(m.shared.User.ID, "analytics")
		if err != nil {
			return true, err
		}
	}

	return enabled, m.fetchFeatures()
}

func findAnalyticsFeature(features []*db.FeatureFlag) *db.FeatureFlag {
	for _, feature := range features {
		if feature.Name == "analytics" {
			return feature
		}
	}
	return nil
}
