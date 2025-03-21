package tui

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils"
)

var HOME = "dash"

type SharedModel struct {
	Logger             *slog.Logger
	Session            *pssh.SSHServerConnSession
	Cfg                *shared.ConfigSite
	Dbpool             db.DB
	User               *db.User
	PlusFeatureFlag    *db.FeatureFlag
	BouncerFeatureFlag *db.FeatureFlag
	Impersonator       string
	App                *vxfw.App
}

type Navigate struct{ To string }
type PageIn struct{}
type PageOut struct{}

var fuschia = vaxis.HexColor(0xEE6FF8)
var cream = vaxis.HexColor(0xFFFDF5)
var green = vaxis.HexColor(0x04B575)
var grey = vaxis.HexColor(0x5C5C5C)
var red = vaxis.HexColor(0xED567A)

// var white = vaxis.HexColor(0xFFFFFF).
var oj = vaxis.HexColor(0xFFCA80)
var purp = vaxis.HexColor(0xBD93F9)
var black = vaxis.HexColor(0x282A36)

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
	shared *SharedModel
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
			if app.page == "signup" || app.page == HOME {
				return nil, nil
			}
			app.shared.App.PostEvent(Navigate{To: HOME})
		}
	}
	return nil, nil
}

type WidgetFooter interface {
	Footer() []Shortcut
}

func (app *App) GetCurPage() vxfw.Widget {
	return app.pages[app.page]
}

func (app *App) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vxfw.Init:
		page := HOME
		// no user? kick them to the create account page
		if app.shared.User == nil {
			page = "signup"
		}
		app.shared.App.PostEvent(Navigate{To: page})
		return nil, nil
	case Navigate:
		cmds := []vxfw.Command{}
		cur := app.GetCurPage()
		if cur != nil {
			// send event to page notifying that we are leaving
			cmd, _ := cur.HandleEvent(PageOut{}, vxfw.TargetPhase)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		// switch the page
		app.page = msg.To

		cur = app.GetCurPage()
		if cur != nil {
			// send event to page notifying that we are entering
			cmd, _ := app.GetCurPage().HandleEvent(PageIn{}, vxfw.TargetPhase)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		cmds = append(
			cmds,
			vxfw.RedrawCmd{},
		)
		return vxfw.BatchCmd(cmds), nil
	}
	return nil, nil
}

func (app *App) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	root := vxfw.NewSurface(w, ctx.Min.Height, app)

	ah := 1
	header := NewHeaderWdt(app.shared, app.page)
	headerSurf, _ := header.Draw(vxfw.DrawContext{
		Max:        vxfw.Size{Width: w, Height: 2},
		Characters: ctx.Characters,
	})
	root.AddChild(1, ah, headerSurf)
	ah += int(headerSurf.Size.Height)

	cur := app.GetCurPage()
	if cur != nil {
		pageCtx := vxfw.DrawContext{
			Characters: ctx.Characters,
			Max:        vxfw.Size{Width: ctx.Max.Width - 1, Height: h - 2 - uint16(ah)},
		}
		surface, _ := app.GetCurPage().Draw(pageCtx)
		root.AddChild(1, ah, surface)
	}

	wdgt, ok := cur.(WidgetFooter)
	segs := []Shortcut{
		{Shortcut: "^c", Text: "quit"},
		{Shortcut: "esc", Text: "prev page"},
	}
	if ok {
		segs = append(segs, wdgt.Footer()...)
	}
	footer := NewFooterWdt(app.shared, segs)
	footerSurf, _ := footer.Draw(vxfw.DrawContext{
		Max:        vxfw.Size{Width: w, Height: 2},
		Characters: ctx.Characters,
	})
	root.AddChild(1, int(ctx.Max.Height)-2, footerSurf)

	return root, nil
}

type HeaderWdgt struct {
	shared *SharedModel

	page string
}

func NewHeaderWdt(shrd *SharedModel, page string) *HeaderWdgt {
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

	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)
	// header
	wdgt := richtext.New([]vaxis.Segment{
		{Text: " " + logoTxt + " ", Style: vaxis.Style{Background: purp, Foreground: black}},
		{Text: " • " + m.page, Style: vaxis.Style{Foreground: green}},
	})
	surf, _ := wdgt.Draw(ctx)
	root.AddChild(0, 0, surf)

	if m.shared.User != nil {
		user := richtext.New([]vaxis.Segment{
			{Text: "~" + m.shared.User.Name, Style: vaxis.Style{Foreground: cream}},
		})
		surf, _ = user.Draw(ctx)
		root.AddChild(int(ctx.Max.Width)-int(surf.Size.Width)-1, 0, surf)
	}

	return root, nil
}

type Shortcut struct {
	Text     string
	Shortcut string
}

type FooterWdgt struct {
	shared *SharedModel

	cmds []Shortcut
}

func NewFooterWdt(shrd *SharedModel, cmds []Shortcut) *FooterWdgt {
	return &FooterWdgt{
		shared: shrd,
		cmds:   cmds,
	}
}

func (m *FooterWdgt) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	return nil, nil
}

func (m *FooterWdgt) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	segs := []vaxis.Segment{}
	for idx, shortcut := range m.cmds {
		segs = append(
			segs,
			vaxis.Segment{Text: shortcut.Shortcut, Style: vaxis.Style{Foreground: fuschia}},
			vaxis.Segment{Text: " " + shortcut.Text},
		)
		if idx < len(m.cmds)-1 {
			segs = append(segs, vaxis.Segment{Text: " • "})
		}
	}
	wdgt := richtext.New(segs)
	return wdgt.Draw(ctx)
}

func initData(shrd *SharedModel) error {
	user, err := FindUser(shrd)
	if err != nil {
		return err
	}
	shrd.User = user

	ff, _ := FindFeatureFlag(shrd, "plus")
	shrd.PlusFeatureFlag = ff

	bff, _ := FindFeatureFlag(shrd, "bouncer")
	shrd.BouncerFeatureFlag = bff
	return nil
}

func FindUser(shrd *SharedModel) (*db.User, error) {
	logger := shrd.Cfg.Logger
	var user *db.User
	usr := shrd.Session.User()

	if shrd.Session.PublicKey() == nil {
		return nil, fmt.Errorf("unable to find public key")
	}

	key := utils.KeyForKeyText(shrd.Session.PublicKey())

	user, err := shrd.Dbpool.FindUserForKey(usr, key)
	if err != nil {
		logger.Error("no user found for public key", "err", err.Error())
		// we only want to throw an error for specific cases
		if errors.Is(err, &db.ErrMultiplePublicKeys{}) {
			return nil, err
		}
		// no user and not error indicates we need to create an account
		return nil, nil
	}
	origUserName := user.Name

	// impersonation
	adminPrefix := "admin__"
	if strings.HasPrefix(usr, adminPrefix) {
		hasFeature := shrd.Dbpool.HasFeatureForUser(user.ID, "admin")
		if !hasFeature {
			return nil, fmt.Errorf("only admins can impersonate a user")
		}
		impersonate := strings.Replace(usr, adminPrefix, "", 1)
		user, err = shrd.Dbpool.FindUserForName(impersonate)
		if err != nil {
			return nil, err
		}
		shrd.Impersonator = origUserName
	}

	return user, nil
}

func FindFeatureFlag(shrd *SharedModel, name string) (*db.FeatureFlag, error) {
	if shrd.User == nil {
		return nil, nil
	}

	ff, err := shrd.Dbpool.FindFeatureForUser(shrd.User.ID, name)
	if err != nil {
		return nil, err
	}

	return ff, nil
}

func NewTui(opts vaxis.Options, shrd *SharedModel) error {
	err := initData(shrd)
	if err != nil {
		return err
	}

	app, err := vxfw.NewApp(opts)
	if err != nil {
		return err
	}

	shrd.App = app
	pages := map[string]vxfw.Widget{
		HOME:         NewMenuPage(shrd),
		"pubkeys":    NewPubkeysPage(shrd),
		"add-pubkey": NewAddPubkeyPage(shrd),
		"tokens":     NewTokensPage(shrd),
		"add-token":  NewAddTokenPage(shrd),
		"signup":     NewSignupPage(shrd),
		"pico+":      NewPlusPage(shrd),
		"logs":       NewLogsPage(shrd),
		"analytics":  NewAnalyticsPage(shrd),
		"chat":       NewChatPage(shrd),
		"tuns":       NewTunsPage(shrd),
	}
	root := &App{
		shared: shrd,
		pages:  pages,
	}

	return app.Run(root)
}
