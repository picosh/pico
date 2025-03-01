package tuivax

import (
	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"github.com/picosh/pico/tui"
	"github.com/picosh/pico/tui/chat"
	"github.com/picosh/pico/tui/common"
)

type Navigate struct{ To string }

var fuschia = vaxis.HexColor(0xEE6FF8)
var cream = vaxis.HexColor(0xFFFDF5)
var indigo = vaxis.HexColor(0x7571F9)
var green = vaxis.HexColor(0x04B575)
var grey = vaxis.HexColor(0x5C5C5C)
var red = vaxis.HexColor(0xED567A)
var white = vaxis.HexColor(0xFFFFFF)
var oj = vaxis.HexColor(0xFFCA80)

func createDrawCtx(ctx vxfw.DrawContext, h uint16) vxfw.DrawContext {
	return vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  ctx.Max.Width,
			Height: h,
		},
	}
}

type App struct {
	shared *common.SharedModel
	pages  map[string]vxfw.Widget
	page   string
}

func (app *App) CaptureEvent(ev vaxis.Event) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vaxis.Key:
		if msg.Matches('c', vaxis.ModCtrl) {
			return vxfw.QuitCmd{}, nil
		}
		if msg.Matches(vaxis.KeyEsc) {
			app.shared.App.PostEvent(Navigate{To: "menu"})
		}
	}
	return nil, nil
}

func (app *App) GetCurPage() vxfw.Widget {
	return app.pages[app.page]
}

func (app *App) focus() vxfw.Command {
	return vxfw.FocusWidgetCmd(app.GetCurPage())
}

func (app *App) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vxfw.Init:
		return vxfw.BatchCmd([]vxfw.Command{
			app.focus(),
			vxfw.RedrawCmd{},
		}), nil
	case Navigate:
		app.page = msg.To
		return vxfw.BatchCmd([]vxfw.Command{
			app.focus(),
			vxfw.RedrawCmd{},
		}), nil
	}
	return nil, nil
}

func (app *App) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	root := vxfw.NewSurface(w, ctx.Min.Height, app)

	header := NewHeaderWdt(app.shared, app.page)
	headerSurf, _ := header.Draw(vxfw.DrawContext{
		Max:        vxfw.Size{Width: w, Height: 2},
		Characters: ctx.Characters,
	})
	root.AddChild(1, 1, headerSurf)

	pageCtx := createDrawCtx(ctx, h-2)
	surface, _ := app.GetCurPage().Draw(pageCtx)
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
	pages := map[string]vxfw.Widget{
		"menu":           NewMenuPage(shrd),
		"pubkeys":        NewPubkeysPage(shrd),
		"add-pubkey":     NewAddPubkeyPage(shrd),
		"tokens":         NewTokensPage(shrd),
		"add-token":      NewAddTokenPage(shrd),
		"create-account": NewCreateAccountPage(shrd),
		"settings":       NewSettingsPage(shrd),
		"pico+":          NewPlusPage(shrd),
	}
	root := &App{
		shared: shrd,
		pages:  pages,
		page:   page,
	}

	err = app.Run(root)
	if err != nil {
		panic(err)
	}
}
