package tuivax

import (
	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
)

type KvData struct {
	data    map[string]string
	Builder func(string, string) (vxfw.Widget, vxfw.Widget)
}

func defaultBuilder(key string, value string) (vxfw.Widget, vxfw.Widget) {
	return text.New(key), text.New(value)
}

func NewKv(data map[string]string) *KvData {
	return &KvData{
		data:    data,
		Builder: defaultBuilder,
	}
}

func (m *KvData) HandleEvent(vaxis.Event, vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (m *KvData) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)
	lng := m.findKeyMaxLen()
	left := vxfw.NewSurface(uint16(lng), ctx.Max.Height, m)
	right := vxfw.NewSurface(ctx.Max.Width-uint16(lng), ctx.Max.Height, m)

	ah := 0
	for key, value := range m.data {
		l, r := m.Builder(key, value)
		lft, _ := l.Draw(ctx)
		left.AddChild(0, ah, lft)
		rht, _ := r.Draw(ctx)
		right.AddChild(0, ah, rht)
		ah += 1
	}

	root.AddChild(0, 0, left)
	root.AddChild(lng, 0, right)

	return root, nil
}

func (m *KvData) findKeyMaxLen() int {
	mx := 0
	for k, _ := range m.data {
		if len(k) > mx {
			mx = len(k)
		}
	}
	return mx + 2
}
