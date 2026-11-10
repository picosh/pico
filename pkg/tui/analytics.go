package tui

import (
	"fmt"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/vxfw"
	"go.rockorager.dev/vaxis/vxfw/list"
	"go.rockorager.dev/vaxis/vxfw/richtext"
	"go.rockorager.dev/vaxis/vxfw/text"
)

type SitesLoaded struct {
	Request uint64
	Sites   []*db.VisitUrl
	Err     error
}

type SiteStatsLoaded struct {
	Request  uint64
	Site     string
	Interval string
	Summary  *db.SummaryVisits
	Err      error
}

type AnalyticsPage struct {
	shared *SharedModel

	loadingSites   bool
	loadingDetails bool
	sitesRequest   uint64
	statsRequest   uint64
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
	if m.loadingSites || len(m.sites) == 0 {
		if i != 0 {
			return nil
		}
		if m.loadingSites {
			return text.New("Loading ...")
		}
		return text.New("No sites found")
	}
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
		m.err = m.fetchFeatures()
		m.fetchSites()
		m.focus = "page"
		return vxfw.FocusWidgetCmd(m), nil
	case PageOut:
		m.sitesRequest++
		m.statsRequest++
		m.loadingSites = false
		m.loadingDetails = false
	case SitesLoaded:
		if msg.Request != m.sitesRequest {
			return nil, nil
		}
		m.loadingSites = false
		m.sites = msg.Sites
		m.err = msg.Err
		m.leftPane.SetCursor(0)
		ff := findAnalyticsFeature(m.features)
		if ff == nil || !ff.IsValid() {
			return vxfw.RedrawCmd{}, nil
		}
		m.focus = "sites"
		return vxfw.BatchCmd([]vxfw.Command{
			vxfw.FocusWidgetCmd(&m.leftPane),
			vxfw.RedrawCmd{},
		}), nil
	case SiteStatsLoaded:
		if msg.Request != m.statsRequest {
			return nil, nil
		}
		m.loadingDetails = false
		m.err = msg.Err
		if msg.Err == nil {
			m.stats[msg.Site+":"+msg.Interval] = msg.Summary
		}
		return vxfw.RedrawCmd{}, nil
	case vaxis.Key:
		if msg.EventType == vaxis.EventRelease {
			return nil, nil
		}
		if msg.Matches('f') {
			if m.interval == "day" {
				m.interval = "month"
			} else {
				m.interval = "day"
			}
			m.rightPane.pos = 0
			m.fetchSiteStats(m.selected, m.interval)
			return vxfw.RedrawCmd{}, nil
		}
		if msg.Matches('t') {
			enabled, err := m.toggleAnalytics()
			if err != nil {
				m.err = err
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
			if m.loadingSites {
				return nil, nil
			}
			cursor := int(m.leftPane.Cursor())
			if cursor >= len(m.sites) {
				return nil, nil
			}
			m.selected = m.sites[m.leftPane.Cursor()].Url
			m.rightPane.pos = 0
			m.fetchSiteStats(m.selected, m.interval)
			return vxfw.RedrawCmd{}, nil
		}
		if msg.Matches(vaxis.KeyTab) || msg.Matches(vaxis.KeyTab, vaxis.ModShift) {
			var cmd vxfw.Widget = &m.leftPane
			ff := findAnalyticsFeature(m.features)
			if ff == nil || !ff.IsValid() {
				return nil, nil
			}
			if m.focus == "sites" && m.selected != "" {
				m.focus = "details"
				cmd = m.rightPane
			} else {
				m.focus = "sites"
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
	top := 0
	if m.err != nil {
		errText := text.New(m.err.Error())
		errText.Style = vaxis.Style{Foreground: red}
		surf, _ := errText.Draw(createDrawCtx(ctx, 1))
		root.AddChild(0, 0, surf)
		top = 2
		ctx = createDrawCtx(ctx, max(1, int(ctx.Max.Height)-top))
	}
	ff := findAnalyticsFeature(m.features)
	if ff == nil || !ff.IsValid() {
		surf := m.banner(ctx)
		root.AddChild(0, top, surf)
		return root, nil
	}

	leftPaneW := float32(ctx.Max.Width) * 0.35

	leftPane := NewBorder(&m.leftPane)
	leftPane.Label = "sites"
	m.focusBorder(leftPane)
	leftSurf, _ := leftPane.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  uint16(leftPaneW),
			Height: ctx.Max.Height,
		},
	})

	root.AddChild(0, top, leftSurf)

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
		root.AddChild(int(leftPaneW), top, rightSurf)
	} else {
		rightSurf := vxfw.NewSurface(uint16(rightPaneW), 0, m)

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
			root.AddChild(int(leftPaneW), top, pagerSurf)
			return root, nil
		}

		rightCtx := vxfw.DrawContext{
			Characters: vaxis.Characters,
			Max: vxfw.Size{
				Width:  uint16(rightPaneW) - 2,
				Height: ctx.Max.Height,
			},
		}
		contentCtx := createDrawCtx(rightCtx, max(2, len(data.Intervals), len(data.TopUrls), len(data.NotFoundUrls), len(data.TopReferers))+3)

		detailSurf, _ := m.detail(contentCtx, data.Intervals).Draw(contentCtx)
		rightSurf.AddChild(0, ah, detailSurf)
		ah += int(detailSurf.Size.Height)

		urlSurf, _ := m.urls(contentCtx, data.TopUrls, "urls").Draw(contentCtx)
		rightSurf.AddChild(0, ah, urlSurf)
		ah += int(urlSurf.Size.Height)

		urlSurf, _ = m.urls(contentCtx, data.NotFoundUrls, "not found").Draw(contentCtx)
		rightSurf.AddChild(0, ah, urlSurf)
		ah += int(urlSurf.Size.Height)

		urlSurf, _ = m.urls(contentCtx, data.TopReferers, "referers").Draw(contentCtx)
		rightSurf.AddChild(0, ah, urlSurf)
		ah += int(urlSurf.Size.Height)

		deviceSurf, _ := m.devices(contentCtx, data.Intervals).Draw(contentCtx)
		rightSurf.AddChild(0, ah, deviceSurf)
		ah += int(deviceSurf.Size.Height)

		surf, _ := m.visits(contentCtx, data.Intervals).Draw(contentCtx)
		rightSurf.AddChild(0, ah, surf)
		rightSurf.Size.Height = uint16(ah) + surf.Size.Height

		m.rightPane.Surface = rightSurf
		rightPane := NewBorder(m.rightPane)
		rightPane.Label = "details"
		m.focusBorder(rightPane)
		pagerSurf, _ := rightPane.Draw(rightCtx)

		root.AddChild(int(leftPaneW), top, pagerSurf)
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

func (m *AnalyticsPage) devices(ctx vxfw.DrawContext, intervals []*db.VisitInterval) vxfw.Widget {
	mobile, desktop := 0, 0
	for _, visit := range intervals {
		mobile += visit.MobileVisitors
		desktop += visit.DesktopVisitors
	}
	total := mobile + desktop
	mobilePct := 0.0
	desktopPct := 0.0
	if total > 0 {
		mobilePct = float64(mobile) / float64(total) * 100
		desktopPct = float64(desktop) / float64(total) * 100
	}
	kv := []Kv{
		{Key: "mobile", Value: fmt.Sprintf("%d (%.1f%%)", mobile, mobilePct)},
		{Key: "desktop", Value: fmt.Sprintf("%d (%.1f%%)", desktop, desktopPct)},
	}
	wdgt := NewKv(kv)
	rightPane := NewBorder(wdgt)
	rightPane.Width = ctx.Max.Width
	rightPane.Label = "devices"
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
	m.loadingSites = true
	m.sitesRequest++
	request := m.sitesRequest
	m.leftPane.SetCursor(0)
	opts := &db.SummaryOpts{
		UserID: m.shared.User.ID,
		Origin: shared.StartOfMonth(),
	}
	pool, app := m.shared.Dbpool, m.shared.App
	go func() {
		sites, err := pool.FindVisitSiteList(opts)
		app.PostEvent(SitesLoaded{Request: request, Sites: sites, Err: err})
	}()
}

func (m *AnalyticsPage) fetchSiteStats(site string, interval string) {
	if site == "" {
		return
	}
	m.loadingDetails = true
	m.err = nil
	m.statsRequest++
	request := m.statsRequest
	opts := &db.SummaryOpts{
		Host: site,

		UserID:   m.shared.User.ID,
		Interval: interval,
	}

	if interval == "day" {
		opts.Origin = shared.StartOfMonth()
	} else {
		opts.Origin = shared.StartOfYear()
	}

	pool, app := m.shared.Dbpool, m.shared.App
	go func() {
		summary, err := pool.VisitSummary(opts)
		app.PostEvent(SiteStatsLoaded{Request: request, Site: site, Interval: interval, Summary: summary, Err: err})
	}()
}

func (m *AnalyticsPage) fetchFeatures() error {
	features, err := m.shared.Dbpool.FindFeaturesByUser(m.shared.User.ID)
	m.features = features
	return err
}

func (m *AnalyticsPage) banner(ctx vxfw.DrawContext) vxfw.Surface {
	segs := []vaxis.Segment{
		{
			Text: "Get usage statistics on your blog, blog posts, and pages sites. For example, see unique visitors, most popular URLs, and top referers.\n\n",
		},
		{
			Text: "We do not collect usage statistic unless analytics is enabled. Further, when analytics are disabled we do not purge usage statistics.\n\n",
		},
		{
			Text: "We will only store usage statistics for 1 year from when the event was created.\n\n",
		},
	}

	if m.shared.PlusFeatureFlag == nil {
		style := vaxis.Style{Foreground: red}
		segs = append(segs,
			vaxis.Segment{
				Text:  "Analytics is only available to pico+ users.\n\n",
				Style: style,
			})
	} else {
		style := vaxis.Style{Foreground: green}
		segs = append(segs,
			vaxis.Segment{
				Text:  "Press 't' to enable analytics\n\n",
				Style: style,
			})
	}

	analytics := richtext.New(segs)
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
