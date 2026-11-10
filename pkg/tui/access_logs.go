package tui

import (
	"fmt"
	"time"

	"github.com/picosh/pico/pkg/db"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/vxfw"
	"go.rockorager.dev/vaxis/vxfw/list"
	"go.rockorager.dev/vaxis/vxfw/richtext"
	"go.rockorager.dev/vaxis/vxfw/text"
)

type AccessLogsLoaded struct {
	Request uint64
	Logs    []*db.AccessLog
	Err     error
}

type AccessLogsPage struct {
	shared *SharedModel

	input    *TextInput
	list     *list.Dynamic
	filtered []int
	logs     []*db.AccessLog
	loading  bool
	request  uint64
	err      error
	focus    string
}

func NewAccessLogsPage(shrd *SharedModel) *AccessLogsPage {
	page := &AccessLogsPage{
		shared: shrd,
		input:  NewTextInput("filter logs"),
		focus:  "input",
	}
	page.list = &list.Dynamic{Builder: page.getWidget, DrawCursor: true}
	page.input.input.OnChange = func(string) (vxfw.Command, error) {
		page.filterLogs()
		return vxfw.RedrawCmd{}, nil
	}
	return page
}

func (m *AccessLogsPage) Footer() []Shortcut {
	return []Shortcut{
		{Shortcut: "tab", Text: "focus"},
		{Shortcut: "^r", Text: "refresh"},
		{Shortcut: "g", Text: "top"},
		{Shortcut: "G", Text: "bottom"},
	}
}

func (m *AccessLogsPage) filterLogLine(match string, ll *db.AccessLog) bool {
	if match == "" {
		return true
	}

	serviceMatch := matched(ll.Service, match)
	identityMatch := matched(ll.Identity, match)
	pubkeyMatch := matched(ll.Pubkey, match)

	return serviceMatch || identityMatch || pubkeyMatch
}

func (m *AccessLogsPage) filterLogs() {
	if m.loading || len(m.logs) == 0 {
		m.filtered = []int{}
		m.list.SetCursor(0)
		return
	}

	match := m.input.GetValue()
	filtered := []int{}
	for idx, ll := range m.logs {
		if m.filterLogLine(match, ll) {
			filtered = append(filtered, idx)
		}
	}

	m.filtered = filtered

	if len(filtered) > 0 {
		m.list.SetCursor(uint(len(filtered) - 1))
	} else {
		m.list.SetCursor(0)
	}
}

func (m *AccessLogsPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		m.fetchLogs()
		m.focus = "input"
		return m.input.FocusIn()
	case PageOut:
		m.request++
		m.loading = false
	case AccessLogsLoaded:
		if msg.Request != m.request {
			return nil, nil
		}
		m.loading = false
		m.logs = msg.Logs
		m.err = msg.Err
		m.filterLogs()
		return vxfw.RedrawCmd{}, nil
	case vaxis.Key:
		if msg.Matches(vaxis.KeyTab) {
			if m.focus == "input" {
				m.focus = "access logs"
				cmd, _ := m.input.FocusOut()
				return vxfw.BatchCmd([]vxfw.Command{
					vxfw.FocusWidgetCmd(m.list),
					cmd,
				}), nil
			}
			m.focus = "input"
			cmd, _ := m.input.FocusIn()
			return vxfw.BatchCmd([]vxfw.Command{
				cmd,
				vxfw.RedrawCmd{},
			}), nil
		}
		if msg.Matches('r', vaxis.ModCtrl) {
			m.fetchLogs()
			return vxfw.RedrawCmd{}, nil
		}
		if msg.Matches('g') {
			// Go to top
			if len(m.filtered) > 0 {
				m.list.SetCursor(0)
				return vxfw.RedrawCmd{}, nil
			}
		}
		if msg.Matches('G') {
			// Go to bottom
			if len(m.filtered) > 0 {
				m.list.SetCursor(uint(len(m.filtered) - 1))
				return vxfw.RedrawCmd{}, nil
			}
		}
	}
	return nil, nil
}

func (m *AccessLogsPage) fetchLogs() {
	m.request++
	if m.shared.User == nil {
		m.err = fmt.Errorf("no user found")
		m.loading = false
		return
	}

	m.loading = true
	m.err = nil
	m.list.SetCursor(0)
	request := m.request
	userID := m.shared.User.ID
	pool, app := m.shared.Dbpool, m.shared.App
	fromDate := time.Now().AddDate(0, 0, -30)
	go func() {
		logs, err := pool.FindAccessLogs(userID, &fromDate)
		app.PostEvent(AccessLogsLoaded{Request: request, Logs: logs, Err: err})
	}()
}

func (m *AccessLogsPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	root := vxfw.NewSurface(w, h, m)
	ah := 0

	info := text.New("Access logs in the last 30 days.  Access logs show SSH connections to pico services.")
	brd := NewBorder(info)
	brd.Label = "desc"
	brdSurf, _ := brd.Draw(ctx)
	root.AddChild(0, ah, brdSurf)
	ah += int(brdSurf.Size.Height)

	listPane := NewBorder(m.list)
	listPane.Label = "access logs"
	m.focusBorder(listPane)
	listSurf, _ := listPane.Draw(createDrawCtx(ctx, int(ctx.Max.Height)-ah-3))
	root.AddChild(0, ah, listSurf)
	ah += int(listSurf.Size.Height)

	inp, _ := m.input.Draw(createDrawCtx(ctx, 4))
	root.AddChild(0, ah, inp)

	return root, nil
}

func (m *AccessLogsPage) focusBorder(brd *Border) {
	focus := m.focus
	if focus == brd.Label {
		brd.Style = vaxis.Style{Foreground: oj}
	} else {
		brd.Style = vaxis.Style{Foreground: purp}
	}
}

func (m *AccessLogsPage) getWidget(i uint, cursor uint) vxfw.Widget {
	if m.loading || m.err != nil || len(m.filtered) == 0 {
		if i != 0 {
			return nil
		}
		if m.loading {
			return text.New("Loading ...")
		}
		if m.err != nil {
			txt := text.New(fmt.Sprintf("ERROR: %s", m.err))
			txt.Style = vaxis.Style{Foreground: red}
			return txt
		}
		if len(m.logs) == 0 {
			return text.New("No access logs found.")
		}
		return text.New("No logs match filter.")
	}

	if int(i) >= len(m.filtered) {
		return nil
	}

	idx := m.filtered[i]
	return accessLogToWidget(m.logs[idx])
}

func accessLogToWidget(log *db.AccessLog) vxfw.Widget {
	dateStr := ""
	if log.CreatedAt != nil {
		dateStr = log.CreatedAt.Format(time.RFC3339)
	}

	segs := []vaxis.Segment{
		{Text: dateStr + " "},
		{Text: log.Service + " ", Style: vaxis.Style{Foreground: purp}},
		{Text: log.Identity + " ", Style: vaxis.Style{Foreground: green}},
	}

	if log.Pubkey != "" {
		fingerprint := log.Pubkey
		if len(fingerprint) > 16 {
			fingerprint = fingerprint[0:25] + "..." + fingerprint[len(fingerprint)-15:]
		}
		segs = append(segs, vaxis.Segment{
			Text:  fingerprint,
			Style: vaxis.Style{Foreground: grey},
		})
	}

	txt := richtext.New(segs)
	return txt
}
