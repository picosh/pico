package tui

import (
	"fmt"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/button"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"git.sr.ht/~rockorager/vaxis/vxfw/textfield"
	"github.com/picosh/pico/db"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
)

type CreateAccountPage struct {
	shared *SharedModel
	focus  string
	err    error

	input *textfield.TextField
	btn   *button.Button
}

func NewCreateAccountPage(shrd *SharedModel) *CreateAccountPage {
	btn := button.New("CREATE", func() (vxfw.Command, error) { return nil, nil })
	btn.Style = button.StyleSet{
		Default: vaxis.Style{Background: grey},
		Focus:   vaxis.Style{Background: fuschia},
	}
	input := textfield.New()
	return &CreateAccountPage{shared: shrd, btn: btn, input: input}
}

func (m *CreateAccountPage) createAccount(name string) (*db.User, error) {
	if name == "" {
		return nil, fmt.Errorf("name is invalid")
	}
	key := utils.KeyForKeyText(m.shared.Session.PublicKey())
	return m.shared.Dbpool.RegisterUser(name, key, "")
}

func (m *CreateAccountPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		return vxfw.FocusWidgetCmd(m.input), nil
	case vaxis.Key:
		if msg.Matches(vaxis.KeyTab) {
			focus := vxfw.FocusWidgetCmd(m.input)
			if m.focus == "button" {
				m.focus = "input"
			} else {
				m.focus = "button"
				focus = vxfw.FocusWidgetCmd(m.btn)
			}
			return vxfw.BatchCmd([]vxfw.Command{
				focus,
				vxfw.RedrawCmd{},
			}), nil
		}
		if msg.Matches(vaxis.KeyEnter) {
			if m.focus == "button" {
				user, err := m.createAccount(m.input.Value)
				if err != nil {
					m.err = err
					return vxfw.RedrawCmd{}, nil
				}
				m.shared.User = user
				m.shared.App.PostEvent(Navigate{To: "menu"})
			}
		}
	}

	return nil, nil
}

func (m *CreateAccountPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height

	root := vxfw.NewSurface(w, h, m)

	fp := ssh.FingerprintSHA256(m.shared.Session.PublicKey())
	intro := richtext.New([]vaxis.Segment{
		{
			Text: "Welcome to pico.sh's management TUI!\n\nBy creating an account you get access to our pico services.  We have free and paid services.  After you create an account, you can go to the Settings page to see which services you can access.\n\n",
		},
		{Text: fmt.Sprintf("pubkey: %s\n\n", fp)},
	})
	intro.Softwrap = false
	introSurf, _ := intro.Draw(ctx)
	ah := 0
	root.AddChild(0, ah, introSurf)
	ah += int(introSurf.Size.Height)

	inpSurf, _ := m.input.Draw(ctx)
	root.AddChild(0, ah, inpSurf)
	ah += int(inpSurf.Size.Height)

	btnSurf, _ := m.btn.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: 10, Height: 1},
	})
	root.AddChild(0, ah+1, btnSurf)
	ah += int(btnSurf.Size.Height) + 1

	if m.err != nil {
		errTxt := text.New(m.err.Error())
		errSurf, _ := errTxt.Draw(ctx)
		root.AddChild(0, ah, errSurf)
	}

	return root, nil
}
