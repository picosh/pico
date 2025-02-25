package tuivax

import (
	"fmt"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/list"
	"github.com/picosh/pico/tui/common"
)

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
	quit bool
	menu list.List
}

type Navigate struct {
	To string
}

type Quit struct{}

func (ui *UIVx) menuPage(win vaxis.Window, ev vaxis.Event) {
	switch msg := ev.(type) {
	case vaxis.Key:
		switch msg.String() {
		case "Ctrl+c", "q", "Escape":
			ui.quit = true
		case "Down", "j":
			ui.menu.Down()
		case "Up", "k":
			ui.menu.Up()
		case "Enter":
			ui.page = menuChoices[ui.menu.Index()]
		}
	}
	ui.menu.Draw(win)
}

func (ui *UIVx) keysPage(win vaxis.Window, ev vaxis.Event) {
	switch msg := ev.(type) {
	case vaxis.Key:
		switch msg.String() {
		case "Ctrl+c":
			ui.quit = true
		case "q", "Escape":
			ui.page = "menu"
		}
	}
	win.Print(vaxis.Segment{Text: "Hello, World!"})
}

func NewTui(opts vaxis.Options, shared *common.SharedModel) {
	vx, err := vaxis.New(opts)
	if err != nil {
		panic(err)
	}
	defer vx.Close()

	ui := UIVx{
		shared: shared,
		vx:     vx,

		page: "menu",
		menu: list.New(menuChoices),
	}

	for ev := range vx.Events() {
		win := vx.Window()
		win.Clear()

		// header
		win.Print(vaxis.Segment{
			Text: fmt.Sprintf("pico.sh • %s", ui.page),
		})

		// page window
		width, height := win.Size()
		pageWin := win.New(0, 2, width, height-2)

		switch ui.page {
		case "menu":
			ui.menuPage(pageWin, ev)
		case "pubkeys":
			ui.keysPage(pageWin, ev)
		}

		if ui.quit {
			return
		}

		vx.Render()
	}
}
