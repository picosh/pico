package tui

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/vxfw"
)

type tuiTestApp struct{ events chan vaxis.Event }

func (a *tuiTestApp) PostEvent(ev vaxis.Event) { a.events <- ev }
func (a *tuiTestApp) Suspend() error           { return nil }
func (a *tuiTestApp) Resume() error            { return nil }

func nextTuiEvent(t *testing.T, app *tuiTestApp) vaxis.Event {
	t.Helper()
	select {
	case ev := <-app.events:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the UI result")
		return nil
	}
}

func surfaceHasWidget(surface vxfw.Surface, widget vxfw.Widget) bool {
	if surface.Widget == widget {
		return true
	}
	for _, child := range surface.Children {
		if surfaceHasWidget(child.Surface, widget) {
			return true
		}
	}
	return false
}

func assertFocusMounted(t *testing.T, surface vxfw.Surface, cmd vxfw.Command) {
	t.Helper()
	switch cmd := cmd.(type) {
	case vxfw.BatchCmd:
		for _, child := range cmd {
			assertFocusMounted(t, surface, child)
		}
	case vxfw.FocusWidgetCmd:
		if cmd == nil || !surfaceHasWidget(surface, cmd) {
			t.Fatalf("focused widget %T is missing from the rendered page", cmd)
		}
	}
}

type analyticsTestDB struct {
	db.DB
	summary func(*db.SummaryOpts) (*db.SummaryVisits, error)
	sites   func(*db.SummaryOpts) ([]*db.VisitUrl, error)
}

func (d *analyticsTestDB) VisitSummary(opts *db.SummaryOpts) (*db.SummaryVisits, error) {
	return d.summary(opts)
}

func (d *analyticsTestDB) FindVisitSiteList(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	return d.sites(opts)
}

func activeAnalyticsFeature() *db.FeatureFlag {
	expires := time.Now().Add(time.Hour)
	return &db.FeatureFlag{Name: "analytics", ExpiresAt: &expires}
}

func TestAnalyticsOverlappingLoads(t *testing.T) {
	app := &tuiTestApp{events: make(chan vaxis.Event, 4)}
	releaseFirst := make(chan struct{})
	failure := errors.New("fixture statistics failure")
	pool := &analyticsTestDB{summary: func(opts *db.SummaryOpts) (*db.SummaryVisits, error) {
		if opts.Host == "first" {
			<-releaseFirst
		}
		if opts.Host == "failure" {
			return nil, failure
		}
		return &db.SummaryVisits{TopUrls: []*db.VisitUrl{{Url: opts.Host}}}, nil
	}}
	page := NewAnalyticsPage(&SharedModel{Dbpool: pool, User: &db.User{ID: "fixture"}, App: app})
	root := &App{page: "analytics", pages: map[string]vxfw.Widget{"analytics": page}}
	page.features = []*db.FeatureFlag{activeAnalyticsFeature()}
	page.selected = "second"
	page.fetchSiteStats("first", "month")
	page.fetchSiteStats("second", "month")
	ctx := vxfw.DrawContext{Characters: vaxis.Characters, Max: vxfw.Size{Width: 80, Height: 20}}
	for range 10 {
		_, _ = page.Draw(ctx)
	}
	second := nextTuiEvent(t, app)
	if len(page.stats) != 0 || !page.loadingDetails {
		t.Fatal("background request changed UI state before its result was handled")
	}
	cmd, err := root.CaptureEvent(second)
	if err != nil || !commandConsumes(cmd) {
		t.Fatal("root did not dispatch and consume the analytics result")
	}
	if page.stats["second:month"] == nil || page.loadingDetails {
		t.Fatal("current result was not applied")
	}
	close(releaseFirst)
	_, _ = page.HandleEvent(nextTuiEvent(t, app), vxfw.TargetPhase)
	if page.stats["first:month"] != nil || page.err != nil {
		t.Fatal("stale result changed the current statistics")
	}
	page.fetchSiteStats("failure", "month")
	_, _ = page.HandleEvent(nextTuiEvent(t, app), vxfw.TargetPhase)
	if !errors.Is(page.err, failure) || page.loadingDetails {
		t.Fatal("failed request did not leave loading state with an error")
	}
	page.fetchSiteStats("second", "day")
	_, _ = page.HandleEvent(PageOut{}, vxfw.TargetPhase)
	_, _ = page.HandleEvent(nextTuiEvent(t, app), vxfw.TargetPhase)
	if page.stats["second:day"] != nil {
		t.Fatal("request completed after leaving the page changed its state")
	}
}

func TestAnalyticsEmptyAndLoadingFocus(t *testing.T) {
	app := &tuiTestApp{events: make(chan vaxis.Event, 1)}
	pool := &analyticsTestDB{sites: func(*db.SummaryOpts) ([]*db.VisitUrl, error) { return nil, nil }}
	page := NewAnalyticsPage(&SharedModel{Dbpool: pool, User: &db.User{ID: "fixture"}, App: app})
	page.features = []*db.FeatureFlag{activeAnalyticsFeature()}
	page.fetchSites()
	ctx := vxfw.DrawContext{Characters: vaxis.Characters, Max: vxfw.Size{Width: 80, Height: 20}}
	for _, ev := range []vaxis.Event{vaxis.Key{Keycode: vaxis.KeyTab}, nextTuiEvent(t, app), vaxis.Key{Keycode: vaxis.KeyTab}} {
		cmd, err := page.HandleEvent(ev, vxfw.TargetPhase)
		if err != nil {
			t.Fatal(err)
		}
		surface, err := page.Draw(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertFocusMounted(t, surface, cmd)
	}
}

func TestAnalyticsContentHeight(t *testing.T) {
	page := NewAnalyticsPage(&SharedModel{})
	page.features = []*db.FeatureFlag{activeAnalyticsFeature()}
	page.selected = "fixture"
	data := &db.SummaryVisits{}
	for i := range 40 {
		data.TopUrls = append(data.TopUrls, &db.VisitUrl{Url: fmt.Sprintf("/fixture/%d", i)})
	}
	page.stats["fixture:month"] = data
	ctx := vxfw.DrawContext{Characters: vaxis.Characters, Max: vxfw.Size{Width: 100, Height: 20}}
	_, _ = page.Draw(ctx)
	if page.rightPane.Surface.Size.Height < 40 || page.rightPane.Surface.Size.Height > 100 {
		t.Fatalf("statistics content height = %d", page.rightPane.Surface.Size.Height)
	}
	for range 200 {
		_, _ = page.rightPane.HandleEvent(vaxis.Key{Keycode: 'j'}, vxfw.TargetPhase)
	}
	if page.rightPane.pos != -(int(page.rightPane.Surface.Size.Height) - page.rightPane.height) {
		t.Fatal("analytics pager did not stop at the final page")
	}
}
