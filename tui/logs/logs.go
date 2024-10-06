package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	input "github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/pages"
)

type state int

const (
	stateLoading state = iota
	stateReady
)

type logLineLoadedMsg map[string]any
type errMsg error

type Model struct {
	shared   common.SharedModel
	state    state
	logData  []map[string]any
	viewport viewport.Model
	input    input.Model
	sub      chan map[string]any
	ctx      context.Context
	done     context.CancelFunc
	errMsg   error
}

func headerHeight(shrd common.SharedModel) int {
	return shrd.HeaderHeight
}

func headerWidth(w int) int {
	return w - 2
}

var defMsg = "Logs will show up here in realtime as they are generated.  There is no log buffer."

func NewModel(shrd common.SharedModel) Model {
	im := input.New()
	im.Cursor.Style = shrd.Styles.Cursor
	im.Placeholder = "filter logs"
	im.PlaceholderStyle = shrd.Styles.InputPlaceholder
	im.Prompt = shrd.Styles.FocusedPrompt.String()
	im.CharLimit = 50
	im.Focus()

	hh := headerHeight(shrd)
	ww := headerWidth(shrd.Width)
	inputHeight := lipgloss.Height(im.View())
	viewport := viewport.New(ww, shrd.Height-hh-inputHeight)
	viewport.YPosition = hh
	viewport.SetContent(defMsg)

	ctx, cancel := context.WithCancel(shrd.Session.Context())

	return Model{
		shared:   shrd,
		state:    stateLoading,
		viewport: viewport,
		logData:  []map[string]any{},
		input:    im,
		sub:      make(chan map[string]any),
		ctx:      ctx,
		done:     cancel,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.connectLogs(m.sub),
		m.waitForActivity(m.sub),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.viewport.Width = headerWidth(msg.Width)
		inputHeight := lipgloss.Height(m.input.View())
		hh := headerHeight(m.shared)
		m.viewport.Height = msg.Height - hh - inputHeight
		m.viewport.SetContent(logsToStr(m.shared.Styles, m.logData, m.input.Value()))

	case logLineLoadedMsg:
		m.state = stateReady
		m.logData = append(m.logData, msg)
		lng := len(m.logData)
		if lng > 1000 {
			m.logData = m.logData[lng-1000:]
		}
		wasAt := false
		if m.viewport.AtBottom() {
			wasAt = true
		}
		m.viewport.SetContent(logsToStr(m.shared.Styles, m.logData, m.input.Value()))
		if wasAt {
			m.viewport.GotoBottom()
		}
		cmds = append(cmds, m.waitForActivity(m.sub))

	case errMsg:
		m.errMsg = msg

	case pages.NavigateMsg:
		// cancel activity logger
		m.done()
		// reset model
		next := NewModel(m.shared)
		return next, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m, pages.Navigate(pages.MenuPage)
		case "tab":
			if m.input.Focused() {
				m.input.Blur()
			} else {
				cmds = append(cmds, m.input.Focus())
			}
		default:
			m.viewport.SetContent(logsToStr(m.shared.Styles, m.logData, m.input.Value()))
			m.viewport.GotoBottom()
		}
	}
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if m.errMsg != nil {
		return m.shared.Styles.Error.Render(m.errMsg.Error())
	}
	if m.state == stateLoading {
		return defMsg
	}
	return m.viewport.View() + "\n" + m.input.View()
}

func (m Model) waitForActivity(sub chan map[string]any) tea.Cmd {
	return func() tea.Msg {
		select {
		case result := <-sub:
			return logLineLoadedMsg(result)
		case <-m.ctx.Done():
			return nil
		}
	}
}

func (m Model) connectLogs(sub chan map[string]any) tea.Cmd {
	return func() tea.Msg {
		stdoutPipe, err := shared.ConnectToLogs(m.ctx)
		if err != nil {
			return errMsg(err)
		}

		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			parsedData := map[string]any{}

			err := json.Unmarshal([]byte(line), &parsedData)
			if err != nil {
				m.shared.Logger.Error("json unmarshal", "err", err)
				continue
			}

			user := shared.AnyToStr(parsedData, "user")
			if user == m.shared.User.Name {
				sub <- parsedData
			}
		}

		return nil
	}
}

func matched(str, match string) bool {
	prim := strings.ToLower(str)
	mtch := strings.ToLower(match)
	return strings.Contains(prim, mtch)
}

func logToStr(styles common.Styles, data map[string]any, match string) string {
	time := shared.AnyToStr(data, "time")
	service := shared.AnyToStr(data, "service")
	level := shared.AnyToStr(data, "level")
	msg := shared.AnyToStr(data, "msg")
	errMsg := shared.AnyToStr(data, "err")
	status := shared.AnyToFloat(data, "status")
	url := shared.AnyToStr(data, "url")

	if match != "" {
		lvlMatch := matched(level, match)
		msgMatch := matched(msg, match)
		serviceMatch := matched(service, match)
		errMatch := matched(errMsg, match)
		urlMatch := matched(url, match)
		statusMatch := matched(fmt.Sprintf("%d", int(status)), match)
		if !lvlMatch && !msgMatch && !serviceMatch && !errMatch && !urlMatch && !statusMatch {
			return ""
		}
	}

	acc := fmt.Sprintf(
		"%s %s %s %s %s %s %s",
		time,
		service,
		levelView(styles, level),
		msg,
		styles.Error.Render(errMsg),
		statusView(styles, int(status)),
		url,
	)
	return acc
}

func statusView(styles common.Styles, status int) string {
	statusStr := fmt.Sprintf("%d", status)
	if status >= 200 && status < 300 {
		return statusStr
	}
	return styles.Error.Render(statusStr)
}

func levelView(styles common.Styles, level string) string {
	if level == "ERROR" {
		return styles.Error.Render(level)
	}
	return styles.Note.Render(level)
}

func logsToStr(styles common.Styles, data []map[string]any, filter string) string {
	acc := ""
	for _, d := range data {
		res := logToStr(styles, d, filter)
		if res != "" {
			acc += res
			acc += "\n"
		}
	}

	if acc == "" {
		if filter == "" {
			return defMsg
		} else {
			return "No results found for filter provided."
		}
	}

	return acc
}
