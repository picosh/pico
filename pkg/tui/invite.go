package tui

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
	m.btn.OnClick = m.submit
	return m
}

func (m *AddInvitePage) Footer() []Shortcut {
	return []Shortcut{
		{Shortcut: "tab", Text: "focus"},
		{Shortcut: "↑/↓ j/k", Text: "scroll invites"},
		{Shortcut: "enter", Text: "send"},
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
		if msg.EventType == vaxis.EventRelease {
			return nil, nil
		}
		if msg.Matches(vaxis.KeyTab) || msg.Matches(vaxis.KeyTab, vaxis.ModShift) {
			next := "input"
			if msg.Matches(vaxis.KeyTab, vaxis.ModShift) {
				switch m.focus {
				case "input":
					next = "list"
				case "list":
					next = "button"
				}
			} else {
				switch m.focus {
				case "input":
					next = "button"
				case "button":
					next = "list"
				}
			}
			cmd, err := m.setFocus(next)
			return vxfw.BatchCmd{cmd, vxfw.ConsumeEventCmd{}}, err
		}
		if msg.Matches(vaxis.KeyEnter) && m.focus == "input" {
			return m.submit()
		}
	}
	return nil, nil
}

func (m *AddInvitePage) submit() (vxfw.Command, error) {
	m.err = m.addInvite(m.input.GetValue())
	if m.err == nil {
		m.input.Reset()
		m.shared.App.PostEvent(Navigate{To: HOME})
	}
	return vxfw.ConsumeAndRedraw(), nil
}

func (m *AddInvitePage) setFocus(focus string) (vxfw.Command, error) {
	m.focus = focus
	if focus == "input" {
		cmd, err := m.input.FocusIn()
		return vxfw.BatchCmd{cmd, vxfw.RedrawCmd{}}, err
	}
	cmd, _ := m.input.FocusOut()
	var widget vxfw.Widget = m.btn
	if focus == "list" {
		widget = &m.list
	}
	return vxfw.BatchCmd{cmd, vxfw.FocusWidgetCmd(widget), vxfw.RedrawCmd{}}, nil
}

func (m *AddInvitePage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch ev.(type) {
	case PageIn:
		m.err = m.fetchInvites()
		m.list.SetCursor(0)
		m.input.Reset()
		return m.setFocus("input")
	case PageOut:
		return m.input.FocusOut()
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
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("enter a pico username")
	}
	db := m.shared.Dbpool
	user, err := db.FindUserByName(username)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("pico user not found")
	}
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

	header := text.New("Invite a user to pico!  Invitee must already have a pico account.  This grants them `pgs`, `prose` and `pastes` access on their respective free tiers. Only pico+ members or users with an active invite can invite other users.")
	headerSurf, _ := header.Draw(ctx)
	root.AddChild(0, ah, headerSurf)
	ah += int(headerSurf.Size.Height) + 1
	if ah >= int(h) {
		return root, nil
	}

	inputSurf, _ := m.input.Draw(createDrawCtx(ctx, min(4, int(h)-ah)))
	root.AddChild(0, ah, inputSurf)
	ah += int(inputSurf.Size.Height) + 1
	if ah >= int(h) {
		return root, nil
	}

	btnSurf, _ := m.btn.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: 6, Height: 1},
	})
	root.AddChild(0, ah, btnSurf)
	ah += int(btnSurf.Size.Height) + 1
	if ah >= int(h) {
		return root, nil
	}

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

	if ah >= int(h) {
		return root, nil
	}
	count := text.New(fmt.Sprintf("%d invites sent", len(m.invites)))
	countSurf, _ := count.Draw(createDrawCtx(ctx, 1))
	root.AddChild(0, ah, countSurf)
	ah += int(countSurf.Size.Height) + 1
	if ah >= int(h) || w <= 2 {
		return root, nil
	}
	listSurf, _ := m.list.Draw(createDrawCtx(ctx, int(h)-ah))
	if len(m.invites) == 0 {
		emptySurf, _ := text.New("No invites sent yet.").Draw(createDrawCtx(ctx, 1))
		listSurf.AddChild(0, 0, emptySurf)
	}
	root.AddChild(0, ah, listSurf)

	return root, nil
}
