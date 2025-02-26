package tuivax

import (
	"fmt"
	"math"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/border"
	"git.sr.ht/~rockorager/vaxis/widgets/list"
	"git.sr.ht/~rockorager/vaxis/widgets/textinput"
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

type UIVx struct {
	shared *common.SharedModel
	vx     *vaxis.Vaxis

	page string
	menu list.List
}

type Navigate struct{ To string }
type Quit struct{}

func (ui *UIVx) menuPage(win vaxis.Window, ev vaxis.Event) {
	switch msg := ev.(type) {
	case vaxis.Key:
		switch msg.String() {
		case "Ctrl+c", "q", "Escape":
			ui.vx.PostEvent(Quit{})
		case "Down", "j":
			ui.menu.Down()
		case "Up", "k":
			ui.menu.Up()
		case "Enter":
			ui.vx.PostEvent(Navigate{To: menuChoices[ui.menu.Index()]})
		}
	}

	createdAt := ui.shared.User.CreatedAt.Format(time.DateOnly)
	pink := vaxis.Style{Foreground: fuschia}

	segs := []vaxis.Segment{}
	segs = append(
		segs,
		vaxis.Segment{Text: " Username: "}, vaxis.Segment{Text: ui.shared.User.Name, Style: pink},
		vaxis.Segment{Text: "\n"},
		vaxis.Segment{Text: " Joined: "}, vaxis.Segment{Text: createdAt, Style: pink},
	)

	brdH := 2
	if ui.shared.PlusFeatureFlag != nil {
		expiresAt := ui.shared.PlusFeatureFlag.ExpiresAt.Format(time.DateOnly)
		segs = append(segs,
			vaxis.Segment{Text: "\n"},
			vaxis.Segment{Text: " Pico+ Expires: "}, vaxis.Segment{Text: expiresAt, Style: pink},
			vaxis.Segment{Text: "\n"},
		)
		brdH += 1
	}
	w, _ := win.Size()
	desc := win.New(0, 0, w, brdH)
	brd := border.Left(desc, pink)
	brd.Print(segs...)
	offset := brdH + 1

	menuWin := win.New(0, offset, win.Width, win.Height-offset)

	ui.menu.Draw(menuWin)
}

type CreateAccountPage struct {
	ui    UIVx
	input *textinput.Model
	focus string
	msg   string
}

func (m *CreateAccountPage) createAccount(name string) (*db.User, error) {
	if name == "" {
		return nil, fmt.Errorf("name is invalid")
	}
	key := utils.KeyForKeyText(m.ui.shared.Session.PublicKey())
	return m.ui.shared.Dbpool.RegisterUser(name, key, "")
}

func (m *CreateAccountPage) HandleEvent(ev vaxis.Event) {
	if m.focus == "input" {
		m.input.Update(ev)
	}

	switch msg := ev.(type) {
	case vaxis.Key:
		switch msg.String() {
		case "Ctrl+c", "q", "Escape":
			m.ui.vx.PostEvent(Quit{})
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
				m.ui.shared.User = user
				m.ui.vx.PostEvent(Navigate{To: "menu"})
			}
		}
	}
}

func (m *CreateAccountPage) Draw(win vaxis.Window) {
	fp := ssh.FingerprintSHA256(m.ui.shared.Session.PublicKey())
	w, h := win.Size()
	intro := win.New(0, 0, w, h-4)
	logo := ""
	if h > 25 {
		logo = common.LogoView() + "\n\n"
	}
	intro.Print(
		vaxis.Segment{Text: logo},
		vaxis.Segment{
			Text: "Welcome to pico.sh's management TUI!\n\nBy creating an account you get access to our pico services.  We have free and paid services.  After you create an account, you can go to the Settings page to see which services you can access.\n\n",
		},
		vaxis.Segment{
			Text: fmt.Sprintf("pubkey: %s\n\n", fp),
		},
	)
	inp := win.New(0, h-5, w, 1)
	m.input.Draw(inp)

	btnStyle := vaxis.Style{Background: grey}
	if m.focus == "button" {
		btnStyle = vaxis.Style{Background: fuschia}
	}
	submit := win.New(0, h-3, w, 3)
	submit.Print(
		vaxis.Segment{
			Text:  " OK ",
			Style: btnStyle,
		},
		vaxis.Segment{
			Text:  "\n" + m.msg,
			Style: vaxis.Style{Foreground: red},
		},
	)
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
	ui UIVx

	selected int
	keys     []*db.PublicKey
	err      error
	confirm  bool
}

func (m *PubkeysPage) fetchKeys() error {
	keys, err := m.ui.shared.Dbpool.FindKeysForUser(m.ui.shared.User)
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

func (m *PubkeysPage) HandleEvent(ev vaxis.Event) {
	if len(m.keys) == 0 {
		err := m.fetchKeys()
		if err != nil {
			m.err = err
			return
		}
	}

	switch msg := ev.(type) {
	case vaxis.Key:
		switch msg.String() {
		case "Ctrl+c":
			m.ui.vx.PostEvent(Quit{})
		case "q", "Escape":
			m.reset()
			m.ui.vx.PostEvent(Navigate{To: "menu"})
		case "j", "Down":
			m.selected = int(
				math.Min(
					float64(len(m.keys)-1),
					float64(m.selected+1),
				),
			)
		case "k", "Up":
			m.selected = int(
				math.Max(
					0,
					float64(m.selected-1),
				),
			)
		case "x":
			if len(m.keys) < 2 {
				m.err = fmt.Errorf("cannot delete last key")
				return
			}
			m.confirm = true
		case "y":
			if m.confirm {
				m.confirm = false
				m.selected = 0
				err := m.ui.shared.Dbpool.RemoveKeys([]string{m.keys[m.selected].ID})
				if err != nil {
					m.err = err
					return
				}
				m.keys = []*db.PublicKey{}
				err = m.fetchKeys()
				if err != nil {
					m.err = err
				}
			}
		case "n":
			m.confirm = false
		case "c":
			m.ui.vx.PostEvent(Navigate{To: "add-key"})
		}
	}
}

func (m *PubkeysPage) Draw(win vaxis.Window) {
	win.Clear()
	w, h := win.Size()

	paginate := paginateWin(len(m.keys), m.selected, h-4, 4)

	header := win.New(0, 0, w, 2)
	header.Print(
		vaxis.Segment{
			Text: fmt.Sprintf(
				"%d pubkeys • page %d of %d\n",
				len(m.keys),
				paginate.curPage,
				paginate.totalPages,
			),
		},
	)

	for idx := range paginate.itemsPerPage {
		cIdx := idx + paginate.iterOffset
		if cIdx >= len(m.keys) {
			break
		}
		pubkey := m.keys[cIdx]

		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubkey.Key))
		if err != nil {
			win.Print(vaxis.Segment{Text: err.Error() + "\n"})
			continue
		}

		// 4 = 3 rows and a gap
		desc := win.New(0, idx*4+2, w, 3)
		style := vaxis.Style{Foreground: grey}
		isSelected := cIdx == m.selected
		if isSelected {
			style = vaxis.Style{Foreground: fuschia}
		}
		brd := border.Left(desc, style)

		// 3 rows
		brd.Wrap(
			vaxis.Segment{Text: " Name: ", Style: style},
			vaxis.Segment{Text: pubkey.Name + "\n"},

			vaxis.Segment{Text: " Key: ", Style: style},
			vaxis.Segment{Text: ssh.FingerprintSHA256(key) + "\n"},

			vaxis.Segment{Text: " Created: ", Style: style},
			vaxis.Segment{Text: pubkey.CreatedAt.Format(time.DateOnly) + "\n"},
		)
	}

	footer := win.New(0, h-3, w, 3)
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
	footer.Print(segs...)
}

type AddKeyPage struct {
	ui UIVx

	input *textinput.Model
	err   error
	focus string
}

func (m *AddKeyPage) reset() {
	m.err = nil
	m.focus = "input"
	m.input.SetContent("")
}

func (m *AddKeyPage) HandleEvent(ev vaxis.Event) {
	if m.focus == "input" {
		m.input.Update(ev)
	}

	switch msg := ev.(type) {
	case vaxis.Key:
		switch msg.String() {
		case "Ctrl+c":
			m.ui.vx.PostEvent(Quit{})
		case "Escape":
			m.ui.vx.PostEvent(Navigate{To: "pubkeys"})
		case "q":
			if m.focus != "input" {
				m.ui.vx.PostEvent(Navigate{To: "pubkeys"})
			}
		case "Tab":
			if m.focus == "input" {
				m.focus = "button"
			} else {
				m.focus = "input"
			}
		case "Enter":
			if m.focus == "button" {
				err := m.addPubkey(m.input.String())
				m.err = err
				if err == nil {
					m.ui.vx.PostEvent(Navigate{To: "pubkeys"})
				}
			}
		}
	}
}

func (m *AddKeyPage) addPubkey(pubkey string) error {
	pk, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(pubkey))
	if err != nil {
		return err
	}

	key := utils.KeyForKeyText(pk)

	return m.ui.shared.Dbpool.InsertPublicKey(
		m.ui.shared.User.ID, key, comment, nil,
	)
}

func (m *AddKeyPage) Draw(win vaxis.Window) {
	w, h := win.Size()
	intro := win.New(0, 0, w, 3)
	intro.Print(
		vaxis.Segment{Text: "Enter a new public key\n\n"},
	)
	form := win.New(0, 3, w, h-3)
	m.input.Draw(form)
	btnStyle := vaxis.Style{Background: grey}
	if m.focus == "button" {
		btnStyle = vaxis.Style{Background: fuschia}
	}
	form.Println(1, vaxis.Segment{Text: " ADD ", Style: btnStyle})
	if m.err != nil {
		form.Println(2, vaxis.Segment{
			Text:  m.err.Error(),
			Style: vaxis.Style{Foreground: red},
		})
	}
}

func (ui *UIVx) tokensPage(win vaxis.Window, ev vaxis.Event) {
	switch msg := ev.(type) {
	case vaxis.Key:
		switch msg.String() {
		case "Ctrl+c":
			ui.vx.PostEvent(Quit{})
		case "q", "Escape":
			ui.vx.PostEvent(Navigate{To: "menu"})
		}
	}
	win.Print(vaxis.Segment{Text: "TOKENS"})
}

func (ui *UIVx) settingsPage(win vaxis.Window, ev vaxis.Event) {
	switch msg := ev.(type) {
	case vaxis.Key:
		switch msg.String() {
		case "Ctrl+c":
			ui.vx.PostEvent(Quit{})
		case "q", "Escape":
			ui.vx.PostEvent(Navigate{To: "menu"})
		}
	}
	win.Print(vaxis.Segment{Text: "SETTINGS"})
}

func (ui *UIVx) logsPage(win vaxis.Window, ev vaxis.Event) {
	switch msg := ev.(type) {
	case vaxis.Key:
		switch msg.String() {
		case "Ctrl+c":
			ui.vx.PostEvent(Quit{})
		case "q", "Escape":
			ui.vx.PostEvent(Navigate{To: "menu"})
		}
	}
	win.Print(vaxis.Segment{Text: "LOGS"})
}

func (ui *UIVx) analyticsPage(win vaxis.Window, ev vaxis.Event) {
	switch msg := ev.(type) {
	case vaxis.Key:
		switch msg.String() {
		case "Ctrl+c":
			ui.vx.PostEvent(Quit{})
		case "q", "Escape":
			ui.vx.PostEvent(Navigate{To: "menu"})
		}
	}
	win.Print(vaxis.Segment{Text: "ANALYTICS"})
}

func (ui *UIVx) plusPage(win vaxis.Window, ev vaxis.Event) {
	switch msg := ev.(type) {
	case vaxis.Key:
		switch msg.String() {
		case "Ctrl+c":
			ui.vx.PostEvent(Quit{})
		case "q", "Escape":
			ui.vx.PostEvent(Navigate{To: "menu"})
		}
	}
	win.Print(vaxis.Segment{Text: "PLUS"})
}

func (ui *UIVx) loadChat() {
	sp := &chat.SenpaiCmd{
		Shared: ui.shared,
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

	vx, err := vaxis.New(opts)
	if err != nil {
		panic(err)
	}
	defer vx.Close()

	ui := UIVx{
		shared: shrd,
		vx:     vx,

		page: page,
		menu: list.New(menuChoices),
	}

	caPage := CreateAccountPage{
		ui:    ui,
		input: textinput.New(),
		focus: "input",
	}
	pubkeysPage := PubkeysPage{
		ui: ui,
	}
	addKeyPage := AddKeyPage{
		ui:    ui,
		input: textinput.New(),
		focus: "input",
	}

	for ev := range vx.Events() {
		win := vx.Window()
		win.Clear()

		switch msg := ev.(type) {
		case Quit:
			return
		case Navigate:
			pubkeysPage.reset()
			addKeyPage.reset()
			ui.page = msg.To
		}

		width, height := win.Size()
		padWin := win.New(1, 1, width, height)

		logoTxt := "pico.sh"
		ff := ui.shared.PlusFeatureFlag
		if ff != nil && ff.IsValid() {
			logoTxt = "pico+"
		}

		// header
		padWin.Print(
			vaxis.Segment{Text: " " + logoTxt + " ", Style: vaxis.Style{Background: indigo}},
			vaxis.Segment{Text: " • " + ui.page, Style: vaxis.Style{Foreground: green}},
		)

		// page window
		padW, padH := padWin.Size()
		pageWin := padWin.New(0, 2, padW, padH-2)

		switch ui.page {
		case "create-account":
			caPage.HandleEvent(ev)
			caPage.Draw(pageWin)
		case "menu":
			ui.menuPage(pageWin, ev)
		case "pubkeys":
			pubkeysPage.HandleEvent(ev)
			pubkeysPage.Draw(pageWin)
		case "add-key":
			addKeyPage.HandleEvent(ev)
			addKeyPage.Draw(pageWin)
		case "tokens":
			ui.tokensPage(pageWin, ev)
		case "settings":
			ui.settingsPage(pageWin, ev)
		case "logs":
			ui.logsPage(pageWin, ev)
		case "analytics":
			ui.analyticsPage(pageWin, ev)
		case "plus":
			ui.plusPage(pageWin, ev)
		case "chat":
			ui.loadChat()
			return
		}

		vx.Render()
	}
}
