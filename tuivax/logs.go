package tuivax

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/textfield"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/utils"
	pipeLogger "github.com/picosh/utils/pipe/log"
)

type LogLineLoaded struct {
	Line map[string]any
}

type LogsPage struct {
	shared *common.SharedModel

	input *textfield.TextField
	list  *list.Dynamic
	focus string
	logs  []map[string]any
	ctx   context.Context
	done  context.CancelFunc
}

func NewLogsPage(shrd *common.SharedModel) *LogsPage {
	ctx, cancel := context.WithCancel(shrd.Session.Context())
	return &LogsPage{
		shared: shrd,
		input:  textfield.New(),
		ctx:    ctx,
		done:   cancel,
	}
}

func (m *LogsPage) CaptureEvent(ev vaxis.Event) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case vaxis.Key:
		if msg.Matches(vaxis.KeyTab) {
			if m.focus == "list" {
				m.focus = "input"
				return vxfw.FocusWidgetCmd(m.input), nil
			}
			m.focus = "list"
			return vxfw.FocusWidgetCmd(m.list), nil
		}
	}
	return nil, nil
}

func (m *LogsPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		go m.connectToLogs()
	case PageOut:
		m.done()
	case LogLineLoaded:
		m.logs = append(m.logs, msg.Line)
	}
	return nil, nil
}

func (m *LogsPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	root := vxfw.NewSurface(0, 0, m)

	listSurf, _ := m.list.Draw(createDrawCtx(ctx, ctx.Min.Height-1))
	root.AddChild(0, 0, listSurf)

	inp, _ := m.input.Draw(ctx)
	root.AddChild(0, int(ctx.Max.Height)-1, inp)

	return root, nil
}

func (m *LogsPage) getWidget(i uint, cursor uint) vxfw.Widget {
	if int(i) >= len(m.logs) {
		return nil
	}

	log := m.logs[i]
	return logToStr(log, m.input.Value)
}

func (m *LogsPage) connectToLogs() error {
	fmt.Println("connecting to logs")
	conn := shared.NewPicoPipeClient()
	drain, err := pipeLogger.ReadLogs(m.ctx, m.shared.Logger, conn)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(drain)
	scanner.Buffer(make([]byte, 32*1024), 32*1024)
	for scanner.Scan() {
		line := scanner.Text()
		parsedData := map[string]any{}

		err := json.Unmarshal([]byte(line), &parsedData)
		if err != nil {
			m.shared.Logger.Error("json unmarshal", "err", err, "line", line)
			continue
		}

		user := utils.AnyToStr(parsedData, "user")
		userId := utils.AnyToStr(parsedData, "userId")
		if user == m.shared.User.Name || userId == m.shared.User.ID {
			m.shared.App.PostEvent(LogLineLoaded{parsedData})
		}
	}

	return nil
}

func matched(str, match string) bool {
	prim := strings.ToLower(str)
	mtch := strings.ToLower(match)
	return strings.Contains(prim, mtch)
}

func logToStr(data map[string]any, match string) vxfw.Widget {
	rawtime := utils.AnyToStr(data, "time")
	service := utils.AnyToStr(data, "service")
	level := utils.AnyToStr(data, "level")
	msg := utils.AnyToStr(data, "msg")
	errMsg := utils.AnyToStr(data, "err")
	status := utils.AnyToFloat(data, "status")
	url := utils.AnyToStr(data, "url")

	if match != "" {
		lvlMatch := matched(level, match)
		msgMatch := matched(msg, match)
		serviceMatch := matched(service, match)
		errMatch := matched(errMsg, match)
		urlMatch := matched(url, match)
		statusMatch := matched(fmt.Sprintf("%d", int(status)), match)
		if !lvlMatch && !msgMatch && !serviceMatch && !errMatch && !urlMatch && !statusMatch {
			return nil
		}
	}

	date, err := time.Parse(time.RFC3339Nano, rawtime)
	dateStr := rawtime
	if err == nil {
		dateStr = date.Format(time.RFC3339)
	}

	segs := []vaxis.Segment{
		{Text: dateStr},
		{Text: service},
	}

	if level == "ERROR" {
		segs = append(segs, vaxis.Segment{Text: level, Style: vaxis.Style{Background: red}})
	} else {
		segs = append(segs, vaxis.Segment{Text: level})
	}

	segs = append(segs, vaxis.Segment{Text: msg})
	if errMsg != "" {
		segs = append(segs, vaxis.Segment{Text: errMsg, Style: vaxis.Style{Foreground: red}})
	}

	if status > 0 {
		if status >= 200 && status < 300 {
			segs = append(segs, vaxis.Segment{Text: fmt.Sprint("%d", status)})
		} else {
			segs = append(segs,
				vaxis.Segment{
					Text:  fmt.Sprint("%d", status),
					Style: vaxis.Style{Foreground: red},
				})
		}
	}

	segs = append(segs, vaxis.Segment{Text: url})

	txt := richtext.New(segs)
	txt.Softwrap = false
	return txt
}
