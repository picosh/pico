package tui

import (
	"testing"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/vxfw"
)

func TestPagerBounds(t *testing.T) {
	page := NewPager()
	page.Surface = vxfw.NewSurface(20, 30, nil)
	ctx := vxfw.DrawContext{Max: vxfw.Size{Width: 20, Height: 10}}
	_, _ = page.Draw(ctx)
	for range 50 {
		_, _ = page.HandleEvent(vaxis.Key{Keycode: vaxis.KeyDown}, vxfw.TargetPhase)
	}
	if page.pos != -20 {
		t.Fatalf("bottom offset = %d, want -20", page.pos)
	}
	ctx.Max.Height = 25
	_, _ = page.Draw(ctx)
	if page.pos != -5 {
		t.Fatalf("offset after resize = %d, want -5", page.pos)
	}
	page.Surface = vxfw.NewSurface(20, 3, nil)
	_, _ = page.Draw(ctx)
	_, _ = page.HandleEvent(vaxis.Key{Keycode: 'j'}, vxfw.TargetPhase)
	if page.pos != 0 {
		t.Fatalf("short content scrolled to %d", page.pos)
	}
	page.Surface = vxfw.NewSurface(20, 30, nil)
	_, _ = page.Draw(ctx)
	for range 20 {
		_, _ = page.HandleEvent(vaxis.Key{Keycode: 'j'}, vxfw.TargetPhase)
	}
	for range 20 {
		_, _ = page.HandleEvent(vaxis.Key{Keycode: vaxis.KeyUp}, vxfw.TargetPhase)
	}
	if page.pos != 0 {
		t.Fatalf("top offset = %d, want 0", page.pos)
	}
}
