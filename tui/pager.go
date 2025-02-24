package tui

import (
	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
)

type Pager struct {
	Surface vxfw.Surface
	pos     int
}

func NewPager() *Pager {
	return &Pager{}
}

func (m *Pager) HandleEvent(ev vaxis.Event, ph vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vaxis.Key:
		if msg.Matches('j') {
			if m.pos == -1*int(m.Surface.Size.Height) {
				return nil, nil
			}
			m.pos -= 1
			return vxfw.RedrawCmd{}, nil
		}
		if msg.Matches('k') {
			if m.pos == 0 {
				return nil, nil
			}
			m.pos += 1
			return vxfw.RedrawCmd{}, nil
		}
	}
	return nil, nil
}

func (m *Pager) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)
	root.AddChild(0, m.pos, m.Surface)
	return root, nil
}
