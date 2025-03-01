package tuivax

import (
	"fmt"
	"net/url"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"github.com/picosh/pico/tui/common"
)

type PlusPage struct {
	shared *common.SharedModel
}

func NewPlusPage(shrd *common.SharedModel) *PlusPage {
	return &PlusPage{
		shared: shrd,
	}
}

func (m *PlusPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (m *PlusPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	paymentLink := "https://auth.pico.sh/checkout"
	link := fmt.Sprintf("%s/%s", paymentLink, url.QueryEscape(m.shared.User.Name))
	header := vaxis.Style{UnderlineColor: fuschia, UnderlineStyle: vaxis.UnderlineDashed}
	segs := []vaxis.Segment{
		{Text: "SIGNUP", Style: vaxis.Style{Foreground: fuschia}},
		{
			Text:  "\n$2/mo (billed yearly)\n\n",
			Style: vaxis.Style{UnderlineColor: oj, UnderlineStyle: vaxis.UnderlineCurly, Foreground: oj},
		},

		{Text: "- tuns\n"},
		{Text: "  - per-site analytics\n"},
		{Text: "- pgs\n"},
		{Text: "  - per-site analytics\n"},
		{Text: "- prose\n"},
		{Text: "  - blog analytics\n"},
		{Text: "- irc bouncer\n"},
		{Text: "- 10GB total storage\n\n"},

		{Text: "| You can use this same flow to add additional years to your membership,\n"},
		{Text: "| including if you are already a pico+ user.\n\n"},

		{Text: "There are a few ways to purchase a membership. We try our best to provide immediate access to pico+ regardless of payment method.\n"},

		{Text: "\nOnline payment\n\n", Style: header},

		{Text: link + "\n", Style: vaxis.Style{Hyperlink: link}},

		{Text: "\nSnail mail\n\n", Style: header},

		{Text: "Send cash (USD) or check to:\n"},
		{Text: "- pico.sh LLC\n"},
		{Text: "- 206 E Huron St\n"},
		{Text: "- Ann Arbor MI 48104\n"},

		{Text: "\nNotes\n\n", Style: header},

		{Text: "Have any questions? "},
		{Text: "mailto:hello@pico.sh", Style: vaxis.Style{Hyperlink: "mailto:hello@pico.sh"}},
		{Text: " or join "},
		{Text: "https://pico.sh/irc", Style: vaxis.Style{Hyperlink: "https://pico.sh/irc"}},
		{Text: ".\n\n"},

		{Text: "We do not maintain active subscriptions for "},
		{Text: "pico+", Style: vaxis.Style{Foreground: indigo}},
		{Text: ". "},

		{Text: "Every year you must pay again. We do not take monthly payments, you must pay for a year up-front. Pricing is subject to change because we plan on continuing to include more services as we build them."},
	}
	rt, _ := richtext.New(segs).Draw(ctx)
	return rt, nil
}
