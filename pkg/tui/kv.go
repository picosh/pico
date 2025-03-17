package tui

import (
	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
)

type KVBuilder func(uint16) (vxfw.Widget, vxfw.Widget)

type Kv struct {
	Key   string
	Value string
	Style vaxis.Style
}

type KvData struct {
	Data        []Kv
	KeyColWidth int
}

func NewKv(data []Kv) *KvData {
	return &KvData{
		Data:        data,
		KeyColWidth: 15,
	}
}

func (m *KvData) HandleEvent(vaxis.Event, vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (m *KvData) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)
	lng := m.KeyColWidth
	left := vxfw.NewSurface(uint16(lng), ctx.Max.Height, m)
	right := vxfw.NewSurface(ctx.Max.Width-uint16(lng), ctx.Max.Height, m)

	ah := 0
	var idx uint16 = 0
	for _, data := range m.Data {
		key := text.New(data.Key)
		key.Style = data.Style
		value := text.New(data.Value)
		value.Style = data.Style
		lft, _ := key.Draw(ctx)
		left.AddChild(0, ah, lft)
		rht, _ := value.Draw(ctx)
		right.AddChild(0, ah, rht)
		idx += 1
		ah += 1
	}
	root.AddChild(0, 0, left)
	root.AddChild(lng, 0, right)

	root.Size.Width = left.Size.Width + right.Size.Width - 2
	root.Size.Height = uint16(ah)

	return root, nil
}
