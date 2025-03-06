package tuivax

import (
	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
)

type GroupStack struct {
	w   []vxfw.Widget
	Gap int
}

func NewGroupStack(widgets []vxfw.Widget) *GroupStack {
	return &GroupStack{
		w:   widgets,
		Gap: 0,
	}
}

func (m *GroupStack) HandleEvent(vaxis.Event, vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (m *GroupStack) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)
	ah := 0
	for _, wdgt := range m.w {
		surf, _ := wdgt.Draw(ctx)
		root.AddChild(0, ah, surf)
		ah += int(surf.Size.Height) + m.Gap
	}
	return root, nil
}
