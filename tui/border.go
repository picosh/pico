package tui

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
	finlabel := fmt.Sprintf(" %s ", label)
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
		if label != "" && j >= 2 && len(finlabel)+1 >= j {
			char = vaxis.Character{Grapheme: string(finlabel[idx]), Width: 1}
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
	w      vxfw.Widget
	Style  vaxis.Style
	Label  string
	Width  uint16
	Height uint16
}

func NewBorder(w vxfw.Widget) *Border {
	return &Border{
		w:     w,
		Style: vaxis.Style{Foreground: purp},
		Label: "",
	}
}

func (b *Border) HandleEvent(vaxis.Event, vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (b *Border) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	surf, _ := b.w.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  ctx.Max.Width - 2,
			Height: ctx.Max.Height - 3,
		},
	})

	w := surf.Size.Width + 2
	if b.Width > 0 {
		w = b.Width - 2
	}

	h := surf.Size.Height + 2
	if b.Height > 0 {
		h = b.Height - 2
	}

	root := border(
		b.Label,
		vxfw.NewSurface(w, h, b),
		b.Style,
	)
	root.AddChild(1, 1, surf)
	return root, nil
}
