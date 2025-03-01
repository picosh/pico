package tuivax

import (
	"fmt"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/button"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"git.sr.ht/~rockorager/vaxis/vxfw/textfield"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
)

type CreateAccountPage struct {
	shared *common.SharedModel
	focus  string
	err    error

	input *textfield.TextField
	btn   *button.Button
}

func NewCreateAccountPage(shrd *common.SharedModel) *CreateAccountPage {
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

	// intro := win.New(0, 0, w, h-4)
	logo := ""
	if ctx.Max.Height > 25 {
		logo = common.LogoView() + "\n\n"
	}
	fp := ssh.FingerprintSHA256(m.shared.Session.PublicKey())
	intro := richtext.New([]vaxis.Segment{
		{Text: logo},
		{
			Text: "Welcome to pico.sh's management TUI!\n\nBy creating an account you get access to our pico services.  We have free and paid services.  After you create an account, you can go to the Settings page to see which services you can access.\n\n",
		},
		{Text: fmt.Sprintf("pubkey: %s\n\n", fp)},
	})
	introSurf, _ := intro.Draw(createDrawCtx(ctx, h-4))
	root.AddChild(0, 0, introSurf)

	inpSurf, _ := m.input.Draw(createDrawCtx(ctx, 1))
	root.AddChild(0, int(h)-5, inpSurf)

	btnSurf, _ := m.btn.Draw(createDrawCtx(ctx, 1))
	root.AddChild(0, int(h)-3, btnSurf)

	if m.err != nil {
		errTxt := text.New(m.err.Error())
		errSurf, _ := errTxt.Draw(createDrawCtx(ctx, 1))
		root.AddChild(0, int(h)-2, errSurf)
	}

	return root, nil
}
