package tui

import (
	"fmt"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/button"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
)

type PubkeysPage struct {
	shared *SharedModel
	list   list.Dynamic

	keys    []*db.PublicKey
	err     error
	confirm bool
}

func NewPubkeysPage(shrd *SharedModel) *PubkeysPage {
	m := &PubkeysPage{
		shared: shrd,
	}
	m.list = list.Dynamic{DrawCursor: true, Builder: m.getWidget, Gap: 1}
	return m
}

type FetchPubkeys struct{}

func (m *PubkeysPage) Footer() []Shortcut {
	return []Shortcut{
		{Shortcut: "j/k", Text: "choose"},
		{Shortcut: "x", Text: "delete"},
		{Shortcut: "c", Text: "create"},
	}
}

func (m *PubkeysPage) fetchKeys() error {
	keys, err := m.shared.Dbpool.FindKeysForUser(m.shared.User)
	if err != nil {
		return err

	}
	m.keys = keys
	return nil
}

func (m *PubkeysPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		m.err = m.fetchKeys()
		return vxfw.FocusWidgetCmd(&m.list), nil
	case vaxis.Key:
		if msg.Matches('c') {
			m.shared.App.PostEvent(Navigate{To: "add-pubkey"})
		}
		if msg.Matches('x') {
			if len(m.keys) < 2 {
				m.err = fmt.Errorf("cannot delete last key")
			} else {
				m.confirm = true
			}
			return vxfw.RedrawCmd{}, nil
		}
		if msg.Matches('y') {
			if m.confirm {
				m.confirm = false
				err := m.shared.Dbpool.RemoveKeys([]string{m.keys[m.list.Cursor()].ID})
				if err != nil {
					m.err = err
					return nil, nil
				}
				m.err = m.fetchKeys()
				return vxfw.RedrawCmd{}, nil
			}
		}
		if msg.Matches('n') {
			m.confirm = false
			return vxfw.RedrawCmd{}, nil
		}
	}

	return nil, nil
}

func (m *PubkeysPage) getWidget(i uint, cursor uint) vxfw.Widget {
	if int(i) >= len(m.keys) {
		return nil
	}

	style := vaxis.Style{Foreground: grey}
	isSelected := i == cursor
	if isSelected {
		style = vaxis.Style{Foreground: fuschia}
	}

	pubkey := m.keys[i]
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubkey.Key))
	if err != nil {
		m.shared.Logger.Error("parse pubkey", "err", err)
		return nil
	}

	keyStr := pubkey.Key[0:25] + "..." + pubkey.Key[len(pubkey.Key)-15:]

	txt := richtext.New([]vaxis.Segment{
		{Text: "Name: ", Style: style},
		{Text: pubkey.Name + "\n"},

		{Text: "Key: ", Style: style},
		{Text: keyStr + "\n"},

		{Text: "Sha: ", Style: style},
		{Text: ssh.FingerprintSHA256(key) + "\n"},

		{Text: "Created: ", Style: style},
		{Text: pubkey.CreatedAt.Format(time.DateOnly)},
	})

	return txt
}

func (m *PubkeysPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	root := vxfw.NewSurface(w, h, m)
	ah := 0

	info := text.New("Pubkeys are SSH public keys which grant access to your pico account.  You can have many pubkeys associated with your account and they all have the same level of access. You cannot delete all pubkeys since it will revoke all access to the account.")
	brd := NewBorder(info)
	brd.Label = "desc"
	brdSurf, _ := brd.Draw(ctx)
	root.AddChild(0, ah, brdSurf)
	ah += int(brdSurf.Size.Height)

	header := text.New(fmt.Sprintf("%d pubkeys\n\n", len(m.keys)))
	headerSurf, _ := header.Draw(ctx)
	root.AddChild(0, ah, headerSurf)
	ah += int(headerSurf.Size.Height)

	listSurf, _ := m.list.Draw(ctx)
	root.AddChild(0, ah, listSurf)

	segs := []vaxis.Segment{}
	if m.confirm {
		segs = append(segs, vaxis.Segment{
			Text:  "are you sure? y/n\n",
			Style: vaxis.Style{Foreground: red},
		})
	}
	if m.err != nil {
		segs = append(segs, vaxis.Segment{
			Text:  m.err.Error() + "\n",
			Style: vaxis.Style{Foreground: red},
		})
	}
	segs = append(segs, vaxis.Segment{Text: "\n"})

	footer := richtext.New(segs)
	footerSurf, _ := footer.Draw(ctx)
	root.AddChild(0, int(h)-3, footerSurf)

	return root, nil
}

type AddKeyPage struct {
	shared *SharedModel

	err   error
	focus string
	input *TextInput
	btn   *button.Button
}

func NewAddPubkeyPage(shrd *SharedModel) *AddKeyPage {
	btn := button.New("ADD", func() (vxfw.Command, error) { return nil, nil })
	btn.Style = button.StyleSet{
		Default: vaxis.Style{Background: grey},
		Focus:   vaxis.Style{Background: oj, Foreground: black},
	}
	return &AddKeyPage{
		shared: shrd,

		input: NewTextInput("add pubkey"),
		btn:   btn,
	}
}

func (m *AddKeyPage) Footer() []Shortcut {
	return []Shortcut{
		{Shortcut: "tab", Text: "focus"},
		{Shortcut: "shift+click", Text: "select text"},
		{Shortcut: "enter", Text: "add public key"},
	}
}

func (m *AddKeyPage) CaptureEvent(ev vaxis.Event) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vaxis.Key:
		if msg.Matches(vaxis.KeyEnter) {
			err := m.addPubkey(m.input.GetValue())
			m.err = err
			if err == nil {
				m.input.Reset()
				m.shared.App.PostEvent(Navigate{To: "pubkeys"})
				return nil, nil
			}
			return vxfw.RedrawCmd{}, nil
		}
	}
	return nil, nil
}

func (m *AddKeyPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
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

func (m *AddKeyPage) addPubkey(pubkey string) error {
	pk, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(pubkey))
	if err != nil {
		return err
	}

	key := utils.KeyForKeyText(pk)

	return m.shared.Dbpool.InsertPublicKey(
		m.shared.User.ID, key, comment, nil,
	)
}

func (m *AddKeyPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	root := vxfw.NewSurface(w, h, m)
	ah := 0

	header := text.New("Enter a new public key. You can typically grab an SSH pubkey in the `.ssh` folder: cat ~/.ssh/id_ed25519.pub.  You can include the comment as well.")
	headerSurf, _ := header.Draw(ctx)
	root.AddChild(0, ah, headerSurf)
	ah += int(headerSurf.Size.Height) + 1

	inputSurf, _ := m.input.Draw(ctx)
	root.AddChild(0, ah, inputSurf)
	ah += int(headerSurf.Size.Height) + 1

	btnSurf, _ := m.btn.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: 5, Height: 1},
	})
	root.AddChild(0, ah, btnSurf)

	if m.err != nil {
		e := richtext.New([]vaxis.Segment{
			{
				Text:  m.err.Error(),
				Style: vaxis.Style{Foreground: red},
			},
		})
		errSurf, _ := e.Draw(createDrawCtx(ctx, 1))
		root.AddChild(0, ah, errSurf)
	}

	return root, nil
}
