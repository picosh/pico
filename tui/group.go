package tui

import (
	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
)

type GroupStack struct {
	s         []vxfw.Surface
	Gap       int
	Direction string
}

func NewGroupStack(widgets []vxfw.Surface) *GroupStack {
	return &GroupStack{
		s:         widgets,
		Gap:       0,
		Direction: "vertical",
	}
}

func (m *GroupStack) HandleEvent(vaxis.Event, vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (m *GroupStack) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)
	ah := 0
	for _, surf := range m.s {
		if m.Direction == "vertical" {
			root.AddChild(0, ah, surf)
			ah += int(surf.Size.Height) + m.Gap
		} else {
			// horizontal
			root.AddChild(ah, 0, surf)
			ah += int(surf.Size.Width) + m.Gap
		}
	}
	return root, nil
}
