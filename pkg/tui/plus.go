package tui

import (
	"fmt"
	"math"
	"net/url"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
)

type PlusPage struct {
	shared *SharedModel

	pager *Pager
}

func NewPlusPage(shrd *SharedModel) *PlusPage {
	page := &PlusPage{
		shared: shrd,
	}
	page.pager = NewPager()
	return page
}

func (m *PlusPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch ev.(type) {
	case PageIn:
		return vxfw.FocusWidgetCmd(m.pager), nil
	}
	return nil, nil
}

func (m *PlusPage) header(ctx vxfw.DrawContext) vxfw.Surface {
	intro := richtext.New([]vaxis.Segment{
		{
			Text: "$2/mo\n",
			Style: vaxis.Style{
				UnderlineStyle: vaxis.UnderlineCurly,
				UnderlineColor: oj,
				Foreground:     oj,
			},
		},
		{
			Text: "(billed yearly)\n\n",
		},

		{Text: "• tuns\n"},
		{Text: "  • per-site analytics\n"},
		{Text: "• pgs\n"},
		{Text: "  • per-site analytics\n"},
		{Text: "• prose\n"},
		{Text: "  • blog analytics\n"},
		{Text: "• irc bouncer\n"},
		{Text: "• 10GB total storage\n"},
	})
	brd := NewBorder(intro)
	brd.Label = "pico+"
	surf, _ := brd.Draw(ctx)
	return surf
}

func (m *PlusPage) payment(ctx vxfw.DrawContext) vxfw.Surface {
	paymentLink := "https://auth.pico.sh/checkout"
	link := fmt.Sprintf("%s/%s", paymentLink, url.QueryEscape(m.shared.User.Name))
	header := vaxis.Style{Foreground: oj, UnderlineColor: oj, UnderlineStyle: vaxis.UnderlineDashed}
	pay := richtext.New([]vaxis.Segment{
		{Text: "You can use this same flow to add additional years to your membership, including if you are already a pico+ user.\n\n", Style: vaxis.Style{Foreground: green}},

		{Text: "There are a few ways to purchase a membership. We try our best to provide immediate access to pico+ regardless of payment method.\n"},

		{Text: "\nOnline payment\n\n", Style: header},

		{Text: link + "\n", Style: vaxis.Style{Hyperlink: link}},

		{Text: "\nSnail mail\n\n", Style: header},

		{Text: "Send cash (USD) or check to:\n\n"},
		{Text: "• pico.sh LLC\n"},
		{Text: "• 206 E Huron St\n"},
		{Text: "• Ann Arbor MI 48104\n\n"},
		{Text: "Have any questions? Feel free to reach out:\n\n"},
		{Text: "• "}, {Text: "mailto:hello@pico.sh\n", Style: vaxis.Style{Hyperlink: "mailto:hello@pico.sh"}},
		{Text: "• "}, {Text: "https://pico.sh/irc\n", Style: vaxis.Style{Hyperlink: "https://pico.sh/irc"}},
	})
	brd := NewBorder(pay)
	brd.Label = "payment"
	surf, _ := brd.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: 50, Height: math.MaxUint16},
	})
	return surf
}

func (m *PlusPage) notes(ctx vxfw.DrawContext) vxfw.Surface {
	wdgt := richtext.New([]vaxis.Segment{
		{Text: "We do not maintain active subscriptions. "},
		{Text: "Every year you must pay again. We do not take monthly payments, you must pay for a year up-front. Pricing is subject to change."},
	})
	brd := NewBorder(wdgt)
	brd.Label = "notes"
	surf, _ := brd.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: 50, Height: math.MaxUint16},
	})
	return surf
}

func (m *PlusPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	stack := NewGroupStack([]vxfw.Surface{
		m.header(ctx),
		m.notes(ctx),
		m.payment(ctx),
	})
	stack.Gap = 1
	surf, _ := stack.Draw(createDrawCtx(ctx, math.MaxUint16))
	m.pager.Surface = surf
	return m.pager.Draw(ctx)
}
