package tui

import (
	"fmt"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/button"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/db"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
)

type SignupPage struct {
	shared *SharedModel
	focus  string
	err    error

	input *TextInput
	btn   *button.Button
}

func NewSignupPage(shrd *SharedModel) *SignupPage {
	btn := button.New("SIGNUP", func() (vxfw.Command, error) { return nil, nil })
	btn.Style = button.StyleSet{
		Default: vaxis.Style{Background: grey},
		Focus:   vaxis.Style{Background: oj, Foreground: black},
	}
	input := NewTextInput("signup")
	return &SignupPage{shared: shrd, btn: btn, input: input}
}

func (m *SignupPage) createAccount(name string) (*db.User, error) {
	if name == "" {
		return nil, fmt.Errorf("name cannot be empty")
	}
	key := utils.KeyForKeyText(m.shared.Session.PublicKey())
	return m.shared.Dbpool.RegisterUser(name, key, "")
}

func (m *SignupPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		return m.input.FocusIn()
	case vaxis.Key:
		if msg.Matches(vaxis.KeyTab) {
			if m.focus == "button" {
				m.focus = "input"
				return m.input.FocusIn()
			}
			m.focus = "button"
			cmd, _ := m.input.FocusOut()
			return vxfw.BatchCmd([]vxfw.Command{
				cmd,
				vxfw.FocusWidgetCmd(m.btn),
			}), nil
		}
		if msg.Matches(vaxis.KeyEnter) {
			if m.focus == "button" {
				user, err := m.createAccount(m.input.GetValue())
				if err != nil {
					m.err = err
					return vxfw.RedrawCmd{}, nil
				}
				m.shared.User = user
				m.shared.App.PostEvent(Navigate{To: HOME})
			}
		}
	}

	return nil, nil
}

func (m *SignupPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height

	root := vxfw.NewSurface(w, h, m)

	fp := ssh.FingerprintSHA256(m.shared.Session.PublicKey())
	intro := richtext.New([]vaxis.Segment{
		{Text: "Welcome to pico.sh's management TUI!\n\n"},
		{Text: "By creating an account you get access to our pico services.  We have free and paid services."},
		{Text: "  After you create an account, you can go to our docs site to get started:\n\n  "},
		{Text: "https://pico.sh/getting-started\n\n", Style: vaxis.Style{Hyperlink: "https://pico.sh/getting-started"}},
		{Text: fmt.Sprintf("pubkey: %s\n\n", fp), Style: vaxis.Style{Foreground: purp}},
	})
	introSurf, _ := intro.Draw(ctx)
	ah := 0
	root.AddChild(0, ah, introSurf)
	ah += int(introSurf.Size.Height)

	inpSurf, _ := m.input.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: ctx.Max.Width, Height: 4},
	})
	root.AddChild(0, ah, inpSurf)
	ah += int(inpSurf.Size.Height)

	btnSurf, _ := m.btn.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: 10, Height: 1},
	})
	root.AddChild(0, ah, btnSurf)
	ah += int(btnSurf.Size.Height)

	if m.err != nil {
		errTxt := text.New(m.err.Error())
		errTxt.Style = vaxis.Style{Foreground: red}
		errSurf, _ := errTxt.Draw(ctx)
		root.AddChild(0, ah, errSurf)
	}

	return root, nil
}
