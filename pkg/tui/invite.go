package tui

import (
	"time"

	"github.com/picosh/pico/pkg/db"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/vxfw"
	"go.rockorager.dev/vaxis/vxfw/button"
	"go.rockorager.dev/vaxis/vxfw/list"
	"go.rockorager.dev/vaxis/vxfw/richtext"
	"go.rockorager.dev/vaxis/vxfw/text"
)

type AddInvitePage struct {
	shared *SharedModel
	list   list.Dynamic

	err     error
	focus   string
	input   *TextInput
	btn     *button.Button
	invites []*db.Invite
}

func NewAddInvitePage(shrd *SharedModel) *AddInvitePage {
	btn := button.New("SEND", func() (vxfw.Command, error) { return nil, nil })
	btn.Style = button.StyleSet{
		Default: vaxis.Style{Background: grey},
		Focus:   vaxis.Style{Background: oj, Foreground: black},
	}
	m := &AddInvitePage{
		shared: shrd,

		input: NewTextInput("pico user"),
		btn:   btn,
	}
	m.list = list.Dynamic{DrawCursor: true, Builder: m.getWidget, Gap: 1}
	return m
}

func (m *AddInvitePage) Footer() []Shortcut {
	return []Shortcut{
		{Shortcut: "tab", Text: "focus"},
		{Shortcut: "enter", Text: "invite user"},
	}
}

func (m *AddInvitePage) getWidget(i uint, cursor uint) vxfw.Widget {
	if int(i) >= len(m.invites) {
		return nil
	}

	style := vaxis.Style{Foreground: grey}
	isSelected := i == cursor
	if isSelected {
		style = vaxis.Style{Foreground: fuschia}
	}

	invite := m.invites[i]

	txt := richtext.New([]vaxis.Segment{
		{Text: "User: ", Style: style},
		{Text: invite.ToUserName + "\n"},

		{Text: "Invited: ", Style: style},
		{Text: invite.CreatedAt.Format(time.DateOnly)},
	})

	return txt
}

func (m *AddInvitePage) CaptureEvent(ev vaxis.Event) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vaxis.Key:
		if msg.Matches(vaxis.KeyEnter) {
			err := m.addInvite(m.input.GetValue())
			m.err = err
			if err == nil {
				m.input.Reset()
				m.shared.App.PostEvent(Navigate{To: HOME})
				return nil, nil
			}
			return vxfw.RedrawCmd{}, nil
		}
	}
	return nil, nil
}

func (m *AddInvitePage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		m.err = m.fetchInvites()
		m.focus = "input"
		m.input.Reset()
		return m.input.FocusIn()
	case vaxis.Key:
		if msg.Matches(vaxis.KeyTab) {
			if m.focus == "input" {
				m.focus = "button"
				cmd, _ := m.input.FocusOut()
				return vxfw.BatchCmd([]vxfw.Command{
					vxfw.FocusWidgetCmd(m.btn),
					cmd,
				}), nil
			}
			m.focus = "input"
			return m.input.FocusIn()
		}
	}

	return nil, nil
}

func (m *AddInvitePage) fetchInvites() error {
	invites, err := m.shared.Dbpool.FindInvitesByUser(m.shared.User.ID)
	if err != nil {
		return err

	}
	m.invites = invites
	return nil
}

func (m *AddInvitePage) addInvite(username string) error {
	db := m.shared.Dbpool
	user, err := db.FindUserByName(username)
	if err != nil {
		return err
	}

	err = db.InviteUser(m.shared.User.ID, user.ID)
	if err != nil {
		return err
	}

	return m.fetchInvites()
}

func (m *AddInvitePage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	root := vxfw.NewSurface(w, h, m)
	ah := 0

	header := text.New("Invite a user to pico!  Invitee must already have a pico account.  This grants them `pgs` and `prose` access on their respective free tiers. Only pico+ members or users that received an invite can invite other users.")
	headerSurf, _ := header.Draw(ctx)
	root.AddChild(0, ah, headerSurf)
	ah += int(headerSurf.Size.Height) + 1

	inputSurf, _ := m.input.Draw(ctx)
	root.AddChild(0, ah, inputSurf)
	ah += int(headerSurf.Size.Height) + 1

	btnSurf, _ := m.btn.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: 6, Height: 1},
	})
	root.AddChild(0, ah, btnSurf)
	ah += int(btnSurf.Size.Height) + 1

	if m.err != nil {
		e := richtext.New([]vaxis.Segment{
			{
				Text:  m.err.Error(),
				Style: vaxis.Style{Foreground: red},
			},
		})
		errSurf, _ := e.Draw(createDrawCtx(ctx, 1))
		root.AddChild(0, ah, errSurf)
		ah += int(errSurf.Size.Height) + 1
	}

	listSurf, _ := m.list.Draw(createDrawCtx(ctx, ctx.Max.Height-uint16(ah)))
	root.AddChild(0, ah, listSurf)

	return root, nil
}
