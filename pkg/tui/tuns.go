package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils/pipe"
)

type RouteListener struct {
	HttpListeners map[string][]string `json:"httpListeners"`
	Listeners     map[string]string   `json:"listeners"`
	TcpAliases    map[string]string   `json:"tcpAliases"`
}

type TunsClient struct {
	RemoteAddr        string        `json:"remoteAddr"`
	User              string        `json:"user"`
	Version           string        `json:"version"`
	Session           string        `json:"session"`
	Pubkey            string        `json:"pubKey"`
	PubkeyFingerprint string        `json:"pubKeyFingerprint"`
	Listeners         []string      `json:"listeners"`
	RouteListeners    RouteListener `json:"routeListeners"`
}

type TunsClientApi struct {
	Clients map[string]*TunsClient `json:"clients"`
	Status  bool                   `json:"status"`
}

type TunsClientSimple struct {
	TunType           string
	TunAddress        string
	RemoteAddr        string
	User              string
	PubkeyFingerprint string
}

type ResultLog struct {
	ServerID           string              `json:"server_id"`
	User               string              `json:"user"`
	UserId             string              `json:"user_id"`
	CurrentTime        string              `json:"current_time"`
	StartTime          time.Time           `json:"start_time"`
	StartTimePretty    string              `json:"start_time_pretty"`
	RequestTime        string              `json:"request_time"`
	RequestIP          string              `json:"request_ip"`
	RequestMethod      string              `json:"request_method"`
	OriginalRequestURI string              `json:"original_request_uri"`
	RequestHeaders     map[string][]string `json:"request_headers"`
	RequestBody        string              `json:"request_body"`
	ResponseHeaders    map[string][]string `json:"response_headers"`
	ResponseBody       string              `json:"response_body"`
	ResponseCode       int                 `json:"response_code"`
	ResponseStatus     string              `json:"response_status"`
	TunnelID           string              `json:"tunnel_id"`
	TunnelType         string              `json:"tunnel_type"`
	ConnectionType     string              `json:"connection_type"`
	// RequestURL         string              `json:"request_url"`
}

type ResultLogLineLoaded struct {
	Line ResultLog
}

type TunsLoaded struct{}

type EventLogsLoaded struct{}

type TunsPage struct {
	shared *SharedModel

	loading      bool
	err          error
	tuns         []TunsClientSimple
	selected     string
	focus        string
	leftPane     list.Dynamic
	rightPane    *Pager
	logs         []*ResultLog
	logList      list.Dynamic
	ctx          context.Context
	done         context.CancelFunc
	isAdmin      bool
	eventLogs    []*db.TunsEventLog
	eventLogList list.Dynamic

	mu sync.RWMutex
}

func NewTunsPage(shrd *SharedModel) *TunsPage {
	m := &TunsPage{
		shared: shrd,

		rightPane: NewPager(),
	}
	m.leftPane = list.Dynamic{DrawCursor: true, Builder: m.getLeftWidget}
	m.logList = list.Dynamic{DrawCursor: true, Builder: m.getLogWidget}
	m.eventLogList = list.Dynamic{DrawCursor: true, Builder: m.getEventLogWidget}
	return m
}

func (m *TunsPage) getLeftWidget(i uint, cursor uint) vxfw.Widget {
	if int(i) >= len(m.tuns) {
		return nil
	}

	site := m.tuns[i]
	txt := text.New(site.TunAddress)
	txt.Softwrap = false
	return txt
}

func (m *TunsPage) getLogWidget(i uint, cursor uint) vxfw.Widget {
	if int(i) >= len(m.logs) {
		return nil
	}

	log := m.logs[i]
	codestyle := vaxis.Style{Foreground: red}
	if log.ResponseCode >= 200 && log.ResponseCode < 300 {
		codestyle = vaxis.Style{Foreground: green}
	}
	if log.ResponseCode >= 300 && log.ResponseCode < 400 {
		codestyle = vaxis.Style{Foreground: oj}
	}
	txt := richtext.New([]vaxis.Segment{
		{Text: log.CurrentTime + " "},
		{Text: log.ResponseStatus + " ", Style: codestyle},
		{Text: log.RequestTime + " "},
		{Text: log.RequestIP + " "},
		{Text: log.RequestMethod + " ", Style: vaxis.Style{Foreground: purp}},
		{Text: log.OriginalRequestURI},
	})
	txt.Softwrap = false
	return txt
}

func (m *TunsPage) getEventLogWidget(i uint, cursor uint) vxfw.Widget {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if int(i) >= len(m.eventLogs) {
		return nil
	}

	log := m.eventLogs[i]
	style := vaxis.Style{Foreground: green}
	if log.EventType == "disconnect" {
		style = vaxis.Style{Foreground: red}
	}
	txt := richtext.New([]vaxis.Segment{
		{Text: log.CreatedAt.Format(time.RFC3339) + " "},
		{Text: log.EventType + " ", Style: style},
		{Text: log.RemoteAddr},
	})
	txt.Softwrap = false
	return txt
}

func (m *TunsPage) connectToLogs() error {
	ctx, cancel := context.WithCancel(m.shared.Session.Context())
	defer cancel()

	m.ctx = ctx
	m.done = cancel

	conn := shared.NewPicoPipeClient()
	drain, err := pipe.Sub(ctx, m.shared.Logger, conn, "sub tuns-result-drain -k")
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(drain)
	scanner.Buffer(make([]byte, 32*1024), 32*1024)
	for scanner.Scan() {
		line := scanner.Text()
		var parsedData ResultLog

		err := json.Unmarshal([]byte(line), &parsedData)
		if err != nil {
			m.shared.Logger.Error("json unmarshal", "err", err, "line", line)
			continue
		}

		if parsedData.TunnelType == "tcp" || parsedData.TunnelType == "sni" {
			newTunID, err := shared.ParseTunsTCP(parsedData.TunnelID, parsedData.ServerID)
			if err != nil {
				m.shared.Logger.Error("parse tun addr", "err", err)
			} else {
				parsedData.TunnelID = newTunID
			}
		}

		user := parsedData.User
		userId := parsedData.UserId
		isUser := user == m.shared.User.Name || userId == m.shared.User.ID

		m.mu.RLock()
		selected := m.selected
		m.mu.RUnlock()
		if (m.isAdmin || isUser) && parsedData.TunnelID == selected {
			m.shared.App.PostEvent(ResultLogLineLoaded{parsedData})
		}
	}

	if err := scanner.Err(); err != nil {
		m.shared.Logger.Error("scanner error", "err", err)
		return err
	}

	return nil
}

func (m *TunsPage) Footer() []Shortcut {
	short := []Shortcut{
		{Shortcut: "j/k", Text: "choose"},
		{Shortcut: "tab", Text: "focus"},
	}
	return short
}

func (m *TunsPage) HandleEvent(ev vaxis.Event, ph vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		m.loading = true
		ff, _ := m.shared.Dbpool.FindFeature(m.shared.User.ID, "admin")
		if ff != nil {
			m.isAdmin = true
		}
		go m.fetchTuns()
		go func() {
			_ = m.connectToLogs()
		}()
		m.focus = "page"
		return vxfw.FocusWidgetCmd(m), nil
	case PageOut:
		m.mu.Lock()
		m.selected = ""
		m.logs = []*ResultLog{}
		m.err = nil
		m.mu.Unlock()
		m.done()
	case ResultLogLineLoaded:
		m.logs = append(m.logs, &msg.Line)
		// scroll to bottom
		if len(m.logs) > 0 {
			m.logList.SetCursor(uint(len(m.logs) - 1))
		}
		return vxfw.RedrawCmd{}, nil
	case EventLogsLoaded:
		return vxfw.RedrawCmd{}, nil
	case TunsLoaded:
		m.focus = "tuns"
		return vxfw.BatchCmd([]vxfw.Command{
			vxfw.FocusWidgetCmd(&m.leftPane),
			vxfw.RedrawCmd{},
		}), nil
	case vaxis.Key:
		if msg.Matches(vaxis.KeyEnter) {
			m.mu.Lock()
			cursor := int(m.leftPane.Cursor())
			if cursor >= len(m.tuns) {
				return nil, nil
			}
			m.selected = m.tuns[m.leftPane.Cursor()].TunAddress
			m.logs = []*ResultLog{}
			m.eventLogs = []*db.TunsEventLog{}
			m.mu.Unlock()
			go m.fetchEventLogs()
			return vxfw.RedrawCmd{}, nil
		}
		if msg.Matches(vaxis.KeyTab) {
			var cmd vxfw.Widget
			if m.focus == "tuns" && m.selected != "" {
				m.focus = "details"
				cmd = m.rightPane
			} else if m.focus == "details" {
				m.focus = "tuns"
				cmd = &m.leftPane
			} else if m.focus == "requests" {
				m.focus = "tuns"
				cmd = &m.leftPane
			} else if m.focus == "page" {
				m.focus = "tuns"
				cmd = &m.leftPane
			}
			return vxfw.BatchCmd([]vxfw.Command{
				vxfw.FocusWidgetCmd(cmd),
				vxfw.RedrawCmd{},
			}), nil
		}
	}
	return nil, nil
}

func (m *TunsPage) focusBorder(brd *Border) {
	focus := m.focus
	if focus == brd.Label {
		brd.Style = vaxis.Style{Foreground: oj}
	} else {
		brd.Style = vaxis.Style{Foreground: purp}
	}
}

func (m *TunsPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	root := vxfw.NewSurface(ctx.Max.Width, ctx.Max.Height, m)
	leftPaneW := float32(ctx.Max.Width) * 0.35

	var wdgt vxfw.Widget = text.New("No tunnels found")
	if len(m.tuns) > 0 {
		wdgt = &m.leftPane
	}

	if m.loading {
		wdgt = text.New("Loading ...")
	}

	leftPane := NewBorder(wdgt)
	leftPane.Label = "tuns"
	m.focusBorder(leftPane)
	leftSurf, _ := leftPane.Draw(vxfw.DrawContext{
		Characters: ctx.Characters,
		Max: vxfw.Size{
			Width:  uint16(leftPaneW),
			Height: ctx.Max.Height - 1,
		},
	})

	root.AddChild(0, 0, leftSurf)

	rightPaneW := float32(ctx.Max.Width) * 0.65
	rightCtx := vxfw.DrawContext{
		Characters: vaxis.Characters,
		Max: vxfw.Size{
			Width:  uint16(rightPaneW) - 2,
			Height: ctx.Max.Height - 1,
		},
	}

	if m.selected == "" {
		rightWdgt := richtext.New([]vaxis.Segment{
			{Text: "This is the pico tuns viewer which will allow users to view all of their tunnels.\n\n"},
			{Text: "Tuns is a pico+ only feature.\n\n", Style: vaxis.Style{Foreground: oj}},
			{Text: "Select a site on the left to view its request logs."},
		})
		brd := NewBorder(rightWdgt)
		brd.Label = "details"
		rightSurf, _ := brd.Draw(rightCtx)
		root.AddChild(int(leftPaneW), 0, rightSurf)
	} else {
		rightSurf := vxfw.NewSurface(uint16(rightPaneW), math.MaxUint16, m)
		ah := 0

		data := m.findSelected()
		if data != nil {
			kv := NewKv([]Kv{
				{Key: "type", Value: data.TunType},
				{Key: "addr", Value: data.TunAddress},
				{Key: "client-addr", Value: data.RemoteAddr},
				{Key: "pubkey", Value: data.PubkeyFingerprint},
			})
			brd := NewBorder(kv)
			brd.Label = "info"

			detailSurf, _ := brd.Draw(rightCtx)
			rightSurf.AddChild(0, ah, detailSurf)
			ah += int(detailSurf.Size.Height)
		}

		brd := NewBorder(&m.logList)
		brd.Label = "requests"
		m.focusBorder(brd)
		surf, _ := brd.Draw(vxfw.DrawContext{
			Characters: vaxis.Characters,
			Max: vxfw.Size{
				Width:  uint16(rightPaneW) - 4,
				Height: 15,
			},
		})
		rightSurf.AddChild(0, ah, surf)
		ah += int(surf.Size.Height)

		brd = NewBorder(&m.eventLogList)
		brd.Label = "conn events"
		m.focusBorder(brd)
		surf, _ = brd.Draw(vxfw.DrawContext{
			Characters: vaxis.Characters,
			Max: vxfw.Size{
				Width:  uint16(rightPaneW) - 4,
				Height: 15,
			},
		})
		rightSurf.AddChild(0, ah, surf)

		m.rightPane.Surface = rightSurf
		rightPane := NewBorder(m.rightPane)
		rightPane.Label = "details"
		m.focusBorder(rightPane)
		pagerSurf, _ := rightPane.Draw(rightCtx)

		root.AddChild(int(leftPaneW), 0, pagerSurf)
	}

	m.mu.RLock()
	if m.err != nil {
		txt := text.New(m.err.Error())
		txt.Style = vaxis.Style{Foreground: red}
		surf, _ := txt.Draw(createDrawCtx(ctx, 1))
		root.AddChild(0, int(ctx.Max.Height-1), surf)
	}
	m.mu.RUnlock()

	return root, nil
}

func (m *TunsPage) findSelected() *TunsClientSimple {
	for _, client := range m.tuns {
		if client.TunAddress == m.selected {
			return &client
		}
	}
	return nil
}

func fetch(fqdn, auth string) (map[string]*TunsClient, error) {
	mapper := map[string]*TunsClient{}
	url := fmt.Sprintf("https://%s/_sish/api/clients?x-authorization=%s", fqdn, auth)
	resp, err := http.Get(url)
	if err != nil {
		return mapper, err
	}
	defer resp.Body.Close()

	var data TunsClientApi
	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return mapper, err
	}

	return data.Clients, nil
}

func (m *TunsPage) fetchEventLogs() {
	logs, err := m.shared.Dbpool.FindTunsEventLogsByAddr(m.shared.User.ID, m.selected)
	if err != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.err = err
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.eventLogs = logs
}

func (m *TunsPage) fetchTuns() {
	tMap, err := fetch("tuns.sh", m.shared.Cfg.TunsSecret)
	if err != nil {
		m.err = err
		return
	}
	nMap, err := fetch("nue.tuns.sh", m.shared.Cfg.TunsSecret)
	if err != nil {
		m.err = err
		return
	}

	ls := []TunsClientSimple{}
	for _, val := range tMap {
		if !m.isAdmin && val.User != m.shared.User.Name {
			continue
		}

		for k := range val.RouteListeners.HttpListeners {
			ls = append(ls, TunsClientSimple{
				TunType:           "http",
				TunAddress:        k,
				RemoteAddr:        val.RemoteAddr,
				User:              val.User,
				PubkeyFingerprint: val.PubkeyFingerprint,
			})
		}

		for k := range val.RouteListeners.TcpAliases {
			ls = append(ls, TunsClientSimple{
				TunType:           "alias",
				TunAddress:        k,
				RemoteAddr:        val.RemoteAddr,
				User:              val.User,
				PubkeyFingerprint: val.PubkeyFingerprint,
			})
		}

		for k := range val.RouteListeners.Listeners {
			tunAddr, err := shared.ParseTunsTCP(k, "tuns.sh")
			if err != nil {
				m.shared.Session.Logger.Info("parse tun addr", "err", err)
				tunAddr = k
			}

			ls = append(ls, TunsClientSimple{
				TunType:           "tcp",
				TunAddress:        tunAddr,
				RemoteAddr:        val.RemoteAddr,
				User:              val.User,
				PubkeyFingerprint: val.PubkeyFingerprint,
			})
		}
	}

	for _, val := range nMap {
		if !m.isAdmin && val.User != m.shared.User.Name {
			continue
		}

		for k := range val.RouteListeners.HttpListeners {
			ls = append(ls, TunsClientSimple{
				TunType:           "http",
				TunAddress:        k,
				RemoteAddr:        val.RemoteAddr,
				User:              val.User,
				PubkeyFingerprint: val.PubkeyFingerprint,
			})
		}

		for k := range val.RouteListeners.TcpAliases {
			ls = append(ls, TunsClientSimple{
				TunType:           "alias",
				TunAddress:        k,
				RemoteAddr:        val.RemoteAddr,
				User:              val.User,
				PubkeyFingerprint: val.PubkeyFingerprint,
			})
		}

		for k := range val.RouteListeners.Listeners {
			tunAddr, err := shared.ParseTunsTCP(k, "nue.tuns.sh")
			if err != nil {
				m.shared.Session.Logger.Info("parse tun addr", "err", err)
				tunAddr = k
			}

			ls = append(ls, TunsClientSimple{
				TunType:           "tcp",
				TunAddress:        tunAddr,
				RemoteAddr:        val.RemoteAddr,
				User:              val.User,
				PubkeyFingerprint: val.PubkeyFingerprint,
			})
		}
	}

	sort.Slice(ls, func(i, j int) bool {
		return ls[i].TunAddress < ls[j].TunAddress
	})

	m.tuns = ls
	m.loading = false
	m.shared.App.PostEvent(TunsLoaded{})
}
