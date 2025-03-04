package tuivax

import (
	"fmt"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
)

var (
	horizontal  = vaxis.Character{Grapheme: "─", Width: 1}
	vertical    = vaxis.Character{Grapheme: "│", Width: 1}
	topLeft     = vaxis.Character{Grapheme: "╭", Width: 1}
	topRight    = vaxis.Character{Grapheme: "╮", Width: 1}
	bottomRight = vaxis.Character{Grapheme: "╯", Width: 1}
	bottomLeft  = vaxis.Character{Grapheme: "╰", Width: 1}
)

func border(label string, surf vxfw.Surface, style vaxis.Style) vxfw.Surface {
	label = fmt.Sprintf(" %s ", label)
	w := surf.Size.Width
	h := surf.Size.Height
	surf.WriteCell(0, 0, vaxis.Cell{
		Character: topLeft,
		Style:     style,
	})
	surf.WriteCell(0, h-1, vaxis.Cell{
		Character: bottomLeft,
		Style:     style,
	})
	surf.WriteCell(w-1, 0, vaxis.Cell{
		Character: topRight,
		Style:     style,
	})
	surf.WriteCell(w-1, h-1, vaxis.Cell{
		Character: bottomRight,
		Style:     style,
	})
	idx := 0
	for j := 1; j < (int(w) - 1); j += 1 {
		i := uint16(j)
		// apply label
		char := horizontal
		if j >= 2 && len(label)+1 >= j {
			char = vaxis.Character{Grapheme: string(label[idx]), Width: 1}
			idx += 1
		}
		surf.WriteCell(i, 0, vaxis.Cell{
			Character: char,
			Style:     style,
		})
		surf.WriteCell(i, h-1, vaxis.Cell{
			Character: horizontal,
			Style:     style,
		})
	}
	for j := 1; j < (int(h) - 1); j += 1 {
		i := uint16(j)
		surf.WriteCell(0, i, vaxis.Cell{
			Character: vertical,
			Style:     style,
		})
		surf.WriteCell(w-1, i, vaxis.Cell{
			Character: vertical,
			Style:     style,
		})
	}

	return surf
}

type Border struct {
	w     vxfw.Widget
	Style vaxis.Style
}

func NewBorder(w vxfw.Widget) *Border {
	return &Border{
		w:     w,
		Style: vaxis.Style{},
	}
}

func (b *Border) HandleEvent(vaxis.Event, vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (b *Border) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	surf, _ := b.w.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  ctx.Max.Width - 3,
			Height: ctx.Max.Height - 3,
		},
	})
	root := border(
		"menu",
		vxfw.NewSurface(ctx.Max.Width-1, ctx.Max.Height-1, b),
		b.Style,
	)
	root.AddChild(1, 1, surf)
	return root, nil
}
