package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/vxfw"
)

func TestInviteEmptyListCanReceiveFocus(t *testing.T) {
	page := NewAddInvitePage(nil)
	surface, err := page.Draw(vxfw.DrawContext{
		Characters: vaxis.Characters,
		Max:        vxfw.Size{Width: 80, Height: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if surface.Children[len(surface.Children)-1].Surface.Widget != &page.list {
		t.Fatal("empty invite list is missing from the focus tree")
	}
}

func TestInviteListFocusAndScroll(t *testing.T) {
	page := NewAddInvitePage(nil)
	_, _ = page.setFocus("input")
	now := time.Now()
	for i := range 30 {
		page.invites = append(page.invites, &db.Invite{ToUserName: fmt.Sprintf("fixture%d", i), CreatedAt: &now})
	}
	for _, want := range []string{"button", "list", "input"} {
		if _, err := page.CaptureEvent(vaxis.Key{Keycode: vaxis.KeyTab}); err != nil {
			t.Fatal(err)
		}
		if page.focus != want || page.input.Focused() != (want == "input") {
			t.Fatalf("focus = %s, want %s", page.focus, want)
		}
	}
	_, _ = page.CaptureEvent(vaxis.Key{Keycode: vaxis.KeyTab, Modifiers: vaxis.ModShift})
	if page.focus != "list" {
		t.Fatal("shift-tab did not focus the list")
	}
	ctx := vxfw.DrawContext{Characters: vaxis.Characters, Max: vxfw.Size{Width: 80, Height: 20}}
	for range 25 {
		if _, err := page.list.CaptureEvent(vaxis.Key{Keycode: vaxis.KeyDown}); err != nil {
			t.Fatal(err)
		}
		if _, err := page.Draw(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if page.list.Cursor() != 25 {
		t.Fatalf("cursor = %d, want 25", page.list.Cursor())
	}
	surface, err := page.Draw(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listSurface := surface.Children[len(surface.Children)-1]
	if listSurface.Surface.Widget != &page.list || listSurface.Origin.Row+int(listSurface.Surface.Size.Height) > int(ctx.Max.Height) {
		t.Fatal("invite list is outside the viewport")
	}
	children := listSurface.Surface.Children
	selected := children[len(children)-1]
	if selected.Origin.Row < 0 || selected.Origin.Row+int(selected.Surface.Size.Height) > int(listSurface.Surface.Size.Height) {
		t.Fatal("selected invite did not scroll into view")
	}
	if _, err := page.CaptureEvent(vaxis.Key{Keycode: vaxis.KeyEnter}); err != nil {
		t.Fatal(err)
	}
	if page.err != nil {
		t.Fatalf("enter on list submitted an invite: %v", page.err)
	}
	for range 25 {
		_, _ = page.list.CaptureEvent(vaxis.Key{Keycode: 'k'})
		_, _ = page.Draw(ctx)
	}
	if page.list.Cursor() != 0 {
		t.Fatalf("cursor = %d after scrolling up", page.list.Cursor())
	}
}
