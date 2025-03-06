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
	"git.sr.ht/~rockorager/vaxis/vxfw/textfield"
	"github.com/picosh/pico/db"
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
	m.list = list.Dynamic{DrawCursor: true, Builder: m.getWidget}
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

	txt := richtext.New([]vaxis.Segment{
		{Text: "Name: ", Style: style},
		{Text: pubkey.Name + "\n"},

		{Text: "Key: ", Style: style},
		{Text: ssh.FingerprintSHA256(key) + "\n"},

		{Text: "Created: ", Style: style},
		{Text: pubkey.CreatedAt.Format(time.DateOnly)},
	})
	txt.Softwrap = false

	return txt
}

func (m *PubkeysPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	root := vxfw.NewSurface(w, h, m)

	header := richtext.New([]vaxis.Segment{
		{
			Text: fmt.Sprintf(
				"%d pubkeys\n",
				len(m.keys),
			),
		},
	})
	headerSurf, _ := header.Draw(createDrawCtx(ctx, 2))
	root.AddChild(0, 0, headerSurf)

	listSurf, _ := m.list.Draw(createDrawCtx(ctx, h-5))
	root.AddChild(0, 3, listSurf)

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
	footerSurf, _ := footer.Draw(createDrawCtx(ctx, 3))
	root.AddChild(0, int(h)-3, footerSurf)

	return root, nil
}

type AddKeyPage struct {
	shared *SharedModel

	err   error
	focus string
	input *textfield.TextField
	btn   *button.Button
}

func NewAddPubkeyPage(shrd *SharedModel) *AddKeyPage {
	btn := button.New("ADD", func() (vxfw.Command, error) { return nil, nil })
	btn.Style = button.StyleSet{
		Default: vaxis.Style{Background: grey},
		Focus:   vaxis.Style{Background: fuschia},
	}
	return &AddKeyPage{
		shared: shrd,

		input: textfield.New(),
		btn:   btn,
	}
}

func (m *AddKeyPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		m.focus = "input"
		m.input.Reset()
		return vxfw.FocusWidgetCmd(m.input), nil
	case vaxis.Key:
		if msg.Matches(vaxis.KeyTab) {
			fcs := vxfw.FocusWidgetCmd(m.input)
			if m.focus == "input" {
				m.focus = "button"
				fcs = vxfw.FocusWidgetCmd(m.btn)
			} else {
				m.focus = "input"
			}
			return vxfw.BatchCmd([]vxfw.Command{
				fcs,
				vxfw.RedrawCmd{},
			}), nil
		}
		if msg.Matches(vaxis.KeyEnter) {
			if m.focus == "button" {
				err := m.addPubkey(m.input.Value)
				m.err = err
				if err == nil {
					m.input.Value = ""
					m.shared.App.PostEvent(Navigate{To: "pubkeys"})
					return nil, nil
				}
				return vxfw.RedrawCmd{}, nil
			}
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

	header := text.New("Enter a new public key")
	headerSurf, _ := header.Draw(createDrawCtx(ctx, 2))
	root.AddChild(0, 0, headerSurf)

	inputSurf, _ := m.input.Draw(createDrawCtx(ctx, 1))
	root.AddChild(0, 3, inputSurf)

	btnSurf, _ := m.btn.Draw(createDrawCtx(ctx, 1))
	root.AddChild(0, 5, btnSurf)

	if m.err != nil {
		e := richtext.New([]vaxis.Segment{
			{
				Text:  m.err.Error(),
				Style: vaxis.Style{Foreground: red},
			},
		})
		errSurf, _ := e.Draw(createDrawCtx(ctx, 1))
		root.AddChild(0, 6, errSurf)
	}

	return root, nil
}
