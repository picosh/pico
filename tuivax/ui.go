package tuivax

import (
	"fmt"
	"math"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"git.sr.ht/~rockorager/vaxis/vxfw/textfield"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui"
	"github.com/picosh/pico/tui/chat"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/utils"
	"golang.org/x/crypto/ssh"
)

var fuschia = vaxis.HexColor(0xEE6FF8)
var cream = vaxis.HexColor(0xFFFDF5)
var indigo = vaxis.HexColor(0x7571F9)
var green = vaxis.HexColor(0x04B575)
var grey = vaxis.HexColor(0x5C5C5C)
var red = vaxis.HexColor(0xED567A)

var menuChoices = []string{
	"pubkeys",
	"tokens",
	"settings",
	"logs",
	"analytics",
	"chat",
	"pico+",
}

type App struct {
	shared *common.SharedModel

	page          string
	menu          *MenuPage
	pubkeys       *PubkeysPage
	createAccount *CreateAccountPage
	addPubkey     *AddKeyPage
}

func (app *App) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vxfw.Init:
		return vxfw.FocusWidgetCmd(app.menu), nil
	case vaxis.Key:
		if msg.Matches('c', vaxis.ModCtrl) {
			return vxfw.QuitCmd{}, nil
		}
	case Navigate:
		app.page = msg.To
		var fcs vxfw.FocusWidgetCmd
		switch app.page {
		case "menu":
			fcs = vxfw.FocusWidgetCmd(app.menu)
		case "pubkeys":
			fcs = vxfw.FocusWidgetCmd(app.pubkeys)
		case "add-pubkey":
			fcs = vxfw.FocusWidgetCmd(app.addPubkey)
		}
		return vxfw.BatchCmd([]vxfw.Command{
			fcs,
			vxfw.RedrawCmd{},
		}), nil
	}
	return nil, nil
}

func (app *App) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	surface := vxfw.Surface{}
	root := vxfw.NewSurface(w, ctx.Min.Height, app)

	header := NewHeaderWdt(app.shared, app.page)
	headerSurf, _ := header.Draw(vxfw.DrawContext{
		Max:        vxfw.Size{Width: w, Height: 2},
		Characters: ctx.Characters,
	})
	root.AddChild(1, 1, headerSurf)
	pageCtx := createDrawCtx(ctx, h-2)

	switch app.page {
	case "menu":
		surface, _ = app.menu.Draw(pageCtx)
	case "pubkeys":
		surface, _ = app.pubkeys.Draw(pageCtx)
	case "add-pubkey":
		surface, _ = app.addPubkey.Draw(pageCtx)
	}

	root.AddChild(1, 3, surface)
	return root, nil
}

type HeaderWdgt struct {
	shared *common.SharedModel

	page string
}

func NewHeaderWdt(shrd *common.SharedModel, page string) *HeaderWdgt {
	return &HeaderWdgt{
		shared: shrd,
		page:   page,
	}
}

func (m *HeaderWdgt) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (m *HeaderWdgt) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	logoTxt := "pico.sh"
	ff := m.shared.PlusFeatureFlag
	if ff != nil && ff.IsValid() {
		logoTxt = "pico+"
	}

	// header
	wdgt := richtext.New([]vaxis.Segment{
		vaxis.Segment{Text: " " + logoTxt + " ", Style: vaxis.Style{Background: indigo}},
		vaxis.Segment{Text: " • " + m.page, Style: vaxis.Style{Foreground: green}},
	})
	return wdgt.Draw(ctx)
}

type Navigate struct{ To string }
type Quit struct{}

type MenuPage struct {
	shared *common.SharedModel

	list list.Dynamic
}

func getMenuWidget(i uint, cursor uint) vxfw.Widget {
	if int(i) >= len(menuChoices) {
		return nil
	}
	var style vaxis.Style
	if i == cursor {
		style.Attribute = vaxis.AttrReverse
	}
	content := menuChoices[i]
	return &text.Text{
		Content: content,
		Style:   style,
	}
}

func NewMenuPage(shrd *common.SharedModel) *MenuPage {
	m := &MenuPage{shared: shrd}
	m.list = list.Dynamic{Builder: getMenuWidget, DrawCursor: true}
	return m
}

func (m *MenuPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vaxis.FocusIn:
		return vxfw.FocusWidgetCmd(&m.list), nil
	case vaxis.Key:
		if msg.Matches(vaxis.KeyEnter) {
			m.shared.App.PostEvent(Navigate{To: menuChoices[m.list.Cursor()]})
		}
	}
	return nil, nil
}

func (m *MenuPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	createdAt := m.shared.User.CreatedAt.Format(time.DateOnly)
	pink := vaxis.Style{Foreground: fuschia}

	segs := []vaxis.Segment{}
	segs = append(
		segs,
		vaxis.Segment{Text: "│", Style: pink},
		vaxis.Segment{Text: " Username: "},
		vaxis.Segment{Text: m.shared.User.Name, Style: pink},
		vaxis.Segment{Text: "\n"},
		vaxis.Segment{Text: "│", Style: pink},
		vaxis.Segment{Text: " Joined: "},
		vaxis.Segment{Text: createdAt, Style: pink},
	)

	brdH := 2
	if m.shared.PlusFeatureFlag != nil {
		expiresAt := m.shared.PlusFeatureFlag.ExpiresAt.Format(time.DateOnly)
		segs = append(segs,
			vaxis.Segment{Text: "\n"},
			vaxis.Segment{Text: "│", Style: pink},
			vaxis.Segment{Text: " Pico+ Expires: "}, vaxis.Segment{Text: expiresAt, Style: pink},
			vaxis.Segment{Text: "\n"},
		)
		brdH += 1
	}

	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)

	infoWdgt := richtext.New(segs)
	infoSurf, _ := infoWdgt.Draw(ctx)
	root.AddChild(0, 0, infoSurf)

	offset := brdH + 1
	listSurf, _ := m.list.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  ctx.Max.Width,
			Height: ctx.Max.Height - uint16(offset),
		},
	})
	root.AddChild(0, offset, listSurf)
	// menuWin := win.New(0, offset, win.Width, win.Height-offset)
	return root, nil
}

type CreateAccountPage struct {
	shared *common.SharedModel
	focus  string
	msg    string
}

func NewCreateAccountPage(shrd *common.SharedModel) *MenuPage {
	return &MenuPage{shared: shrd}
}

func (m *CreateAccountPage) createAccount(name string) (*db.User, error) {
	if name == "" {
		return nil, fmt.Errorf("name is invalid")
	}
	key := utils.KeyForKeyText(m.shared.Session.PublicKey())
	return m.shared.Dbpool.RegisterUser(name, key, "")
}

func (m *CreateAccountPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	/* if m.focus == "input" {
		m.input.Update(ev)
	}

	switch msg := ev.(type) {
	case vaxis.Key:
		switch msg.String() {
		case "Ctrl+c", "q", "Escape":
			// m.ui.vx.PostEvent(Quit{})
		case "Tab":
			if m.focus == "button" {
				m.focus = "input"
			} else {
				m.focus = "button"
			}
		case "Enter":
			// submit
			if m.focus == "button" {
				user, err := m.createAccount(m.input.String())
				if err != nil {
					m.msg = err.Error()
				}
				m.shared.User = user
				// m.ui.vx.PostEvent(Navigate{To: "menu"})
			}
		}
	} */

	return nil, nil
}

func (m *CreateAccountPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	fp := ssh.FingerprintSHA256(m.shared.Session.PublicKey())

	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)

	// intro := win.New(0, 0, w, h-4)
	logo := ""
	if ctx.Max.Height > 25 {
		logo = common.LogoView() + "\n\n"
	}
	intro := richtext.New([]vaxis.Segment{
		{Text: logo},
		{
			Text: "Welcome to pico.sh's management TUI!\n\nBy creating an account you get access to our pico services.  We have free and paid services.  After you create an account, you can go to the Settings page to see which services you can access.\n\n",
		},
		{Text: fmt.Sprintf("pubkey: %s\n\n", fp)},
	})
	introSurf, _ := intro.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: ctx.Max.Width, Height: ctx.Max.Height - 4},
	})

	root.AddChild(0, 0, introSurf)

	inp := text.New("INPUT PLACEHOLDER")
	inpSurf, _ := inp.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: ctx.Max.Width, Height: 1},
	})
	root.AddChild(0, int(ctx.Max.Height)-5, inpSurf)

	btnStyle := vaxis.Style{Background: grey}
	if m.focus == "button" {
		btnStyle = vaxis.Style{Background: fuschia}
	}
	submit := richtext.New(
		[]vaxis.Segment{
			{
				Text:  " OK ",
				Style: btnStyle,
			},
			{
				Text:  "\n" + m.msg,
				Style: vaxis.Style{Foreground: red},
			},
		},
	)
	submitSurf, _ := submit.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: ctx.Max.Width, Height: 3},
	})
	root.AddChild(0, int(ctx.Max.Height)-3, submitSurf)

	return vxfw.Surface{}, nil
}

type PaginateWin struct {
	// max items per page
	itemsPerPage int
	totalPages   int
	curPage      int
	iterOffset   int
}

func paginateWin(size, cur, winH, cellH int) PaginateWin {
	pages := math.Ceil(float64(size*cellH) / float64(winH))
	itemsPerPage := winH / cellH
	curPage := cur / itemsPerPage
	iterOffset := curPage * itemsPerPage
	// can't figure out how to get total pages to work without this
	if pages != 1 {
		pages += 1
	}
	return PaginateWin{
		totalPages:   int(pages),
		curPage:      int(curPage + 1),
		itemsPerPage: int(itemsPerPage),
		iterOffset:   int(iterOffset),
	}
}

type PubkeysPage struct {
	shared *common.SharedModel
	list   list.Dynamic

	selected int
	keys     []*db.PublicKey
	err      error
	confirm  bool
}

func NewPubkeysPage(shrd *common.SharedModel) *PubkeysPage {
	m := &PubkeysPage{
		shared: shrd,
	}
	m.list = list.Dynamic{DrawCursor: true, Builder: m.getWidget}
	return m
}

type FetchPubkeys struct{}

func (m *PubkeysPage) fetchKeys() error {
	keys, err := m.shared.Dbpool.FindKeysForUser(m.shared.User)
	if err != nil {
		return err

	}
	m.keys = keys
	return nil
}

func (m *PubkeysPage) reset() {
	m.confirm = false
	m.err = nil
	m.selected = 0
	m.keys = []*db.PublicKey{}
}

func (m *PubkeysPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vaxis.FocusIn:
		m.err = m.fetchKeys()
		return vxfw.FocusWidgetCmd(&m.list), nil
	case vaxis.Key:
		if msg.Matches('c') {
			m.shared.App.PostEvent(Navigate{To: "add-pubkey"})
		}
		/* switch msg.String() {
		case "q", "Escape":
			m.shared.App.PostEvent(Navigate{To: "menu"})
			// m.ui.vx.PostEvent(Navigate{To: "menu"})
		case "x":
			if len(m.keys) < 2 {
				m.err = fmt.Errorf("cannot delete last key")
				return nil, nil
			}
			m.confirm = true
		case "y":
			if m.confirm {
				m.confirm = false
				m.selected = 0
				err := m.shared.Dbpool.RemoveKeys([]string{m.keys[m.selected].ID})
				if err != nil {
					m.err = err
					return nil, nil
				}
				m.err = m.fetchKeys()
			}
		case "n":
			m.confirm = false
		case "c":
			// m.ui.vx.PostEvent(Navigate{To: "add-key"})
		*/
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
		{Text: " Name: ", Style: style},
		{Text: pubkey.Name + "\n"},

		{Text: " Key: ", Style: style},
		{Text: ssh.FingerprintSHA256(key) + "\n"},

		{Text: " Created: ", Style: style},
		{Text: pubkey.CreatedAt.Format(time.DateOnly)},
	})

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
	headerSurf, _ := header.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: w, Height: 2},
	})
	root.AddChild(0, 0, headerSurf)

	listSurf, _ := m.list.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: w, Height: h - 5},
	})
	root.AddChild(0, 3, listSurf)

	segs := []vaxis.Segment{
		{
			Text:  "j/k, ↑/↓: choose, x: delete, c: create, esc: exit\n",
			Style: vaxis.Style{Foreground: grey},
		},
	}
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
	footerSurf, _ := footer.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max:        vxfw.Size{Width: w, Height: 3},
	})
	root.AddChild(0, int(h)-3, footerSurf)

	return root, nil
}

type AddKeyPage struct {
	shared *common.SharedModel

	err   error
	focus string
	input *textfield.TextField
	btn   *richtext.RichText
}

func NewAddPubkeyPage(shrd *common.SharedModel) *AddKeyPage {
	btnStyle := vaxis.Style{Background: grey}
	return &AddKeyPage{
		shared: shrd,

		input: &textfield.TextField{},
		btn: richtext.New([]vaxis.Segment{
			vaxis.Segment{Text: " ADD ", Style: btnStyle},
		}),
	}
}

func (m *AddKeyPage) reset() {
	m.err = nil
	m.focus = "input"
	// m.input.SetContent("")
}

func (m *AddKeyPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vaxis.FocusIn:
		m.focus = "input"
		return vxfw.FocusWidgetCmd(m.input), nil
	case vaxis.Key:
		if msg.Matches(vaxis.KeyTab) {
			if m.focus == "input" {
				m.focus = "button"
				btnStyle := vaxis.Style{Background: fuschia}
				m.btn = richtext.New([]vaxis.Segment{
					vaxis.Segment{Text: " ADD ", Style: btnStyle},
				})
				return vxfw.FocusWidgetCmd(m.btn), nil
			} else {
				m.focus = "input"
				btnStyle := vaxis.Style{Background: grey}
				m.btn = richtext.New([]vaxis.Segment{
					vaxis.Segment{Text: " ADD ", Style: btnStyle},
				})
				return vxfw.FocusWidgetCmd(m.input), nil
			}
		}
		if msg.Matches(vaxis.KeyEnter) {
			if m.focus == "button" {
				err := m.addPubkey(m.input.Value)
				m.err = err
				if err == nil {
					m.shared.App.PostEvent(Navigate{To: "pubkeys"})
				}
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

func createDrawCtx(ctx vxfw.DrawContext, h uint16) vxfw.DrawContext {
	return vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  ctx.Max.Width,
			Height: h,
		},
	}
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

	return vxfw.Surface{}, nil
}

func loadChat(shrd *common.SharedModel) {
	sp := &chat.SenpaiCmd{
		Shared: shrd,
	}
	sp.Run()
}

func initData(shrd *common.SharedModel) {
	user, err := tui.FindUser(shrd)
	if err != nil {
		panic(err)
	}
	shrd.User = user

	ff, _ := tui.FindPlusFeatureFlag(shrd)
	shrd.PlusFeatureFlag = ff
}

func NewTui(opts vaxis.Options, shrd *common.SharedModel) {
	initData(shrd)
	page := "menu"
	if shrd.User == nil {
		page = "create-account"
	}
	app, err := vxfw.NewApp(opts)
	if err != nil {
		panic(err)
	}

	shrd.App = app
	root := &App{
		shared:    shrd,
		page:      page,
		menu:      NewMenuPage(shrd),
		pubkeys:   NewPubkeysPage(shrd),
		addPubkey: NewAddPubkeyPage(shrd),
	}

	err = app.Run(root)
	if err != nil {
		panic(err)
	}
}
