package tui

import (
	"errors"
	"testing"

	"github.com/picosh/pico/pkg/db"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/vxfw"
)

func TestEmptyLogsFocus(t *testing.T) {
	logs := NewLogsPage(nil)
	access := NewAccessLogsPage(nil)
	ctx := vxfw.DrawContext{Characters: vaxis.Characters, Max: vxfw.Size{Width: 80, Height: 20}}
	for _, state := range []string{"empty", "loading", "error", "filtered"} {
		t.Run(state, func(t *testing.T) {
			logs.focus = "input"
			access.focus = "input"
			access.loading = state == "loading"
			access.err = nil
			if state == "error" {
				access.err = errors.New("fixture failure")
			}
			if state == "filtered" {
				logs.logs = []*LogLine{{Msg: "fixture"}}
				access.logs = []*db.AccessLog{{Service: "fixture"}}
			}
			for _, page := range []vxfw.Widget{logs, access} {
				for range 2 {
					cmd, err := page.(vxfw.EventHandler).HandleEvent(vaxis.Key{Keycode: vaxis.KeyTab}, vxfw.BubblePhase)
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
		})
	}
}

func TestLogFilterUpdatesAfterTyping(t *testing.T) {
	logs := NewLogsPage(nil)
	logs.logs = []*LogLine{{Msg: "matching fixture"}, {Msg: "other"}}
	logs.filterLogs()
	access := NewAccessLogsPage(nil)
	access.logs = []*db.AccessLog{{Service: "matching"}, {Service: "other"}}
	access.filterLogs()
	for _, input := range []*TextInput{logs.input, access.input} {
		_, _ = input.input.HandleEvent(vaxis.Key{Keycode: 'm', Text: "matching"}, vxfw.TargetPhase)
	}
	if len(logs.filtered) != 1 || logs.filtered[0] != 0 || len(access.filtered) != 1 || access.filtered[0] != 0 {
		t.Fatal("filter used the value from before the edit")
	}
}

func TestAccessLogResultsIgnoreStaleRequests(t *testing.T) {
	page := NewAccessLogsPage(nil)
	app := &App{page: "access-logs", pages: map[string]vxfw.Widget{"access-logs": page}}
	page.request = 2
	page.loading = true
	_, _ = app.CaptureEvent(AccessLogsLoaded{Request: 1, Err: errors.New("stale failure")})
	if !page.loading || page.err != nil {
		t.Fatal("stale response changed the loading state")
	}
	cmd, err := app.CaptureEvent(AccessLogsLoaded{Request: 2, Logs: []*db.AccessLog{{Service: "fixture"}}})
	if err != nil || !commandConsumes(cmd) || page.loading || len(page.filtered) != 1 {
		t.Fatal("current response was not applied")
	}
}
