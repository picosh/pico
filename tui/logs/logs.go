package logs

import (
	"bufio"
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

type Model struct {
	shared   common.SharedModel
	state    state
	logData  []map[string]any
	viewport viewport.Model
	input    input.Model
	sub      chan map[string]any
}

func headerHeight(shrd common.SharedModel) int {
	return shrd.HeaderHeight
}

func headerWidth(w int) int {
	return w - 2
}

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

	return Model{
		shared:   shrd,
		state:    stateReady,
		viewport: viewport,
		logData:  []map[string]any{},
		input:    im,
		sub:      make(chan map[string]any),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.connectLogs(m.sub),
		waitForActivity(m.sub),
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
	case logLineLoadedMsg:
		m.logData = append(m.logData, msg)
		lng := len(m.logData)
		if lng > 1000 {
			m.logData = m.logData[lng-1000:]
		}
		wasAt := false
		if m.viewport.AtBottom() {
			wasAt = true
		}
		m.viewport.SetContent(logsToStr(m.logData, m.input.Value()))
		if wasAt {
			m.viewport.GotoBottom()
		}
		cmds = append(cmds, waitForActivity(m.sub))
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
			m.viewport.SetContent(logsToStr(m.logData, m.input.Value()))
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
	if m.state == stateLoading {
		return "Loading ..."
	}
	return m.viewport.View() + "\n" + m.input.View()
}

func waitForActivity(sub chan map[string]any) tea.Cmd {
	return func() tea.Msg {
		result := <-sub
		return logLineLoadedMsg(result)
	}
}

func (m Model) connectLogs(sub chan map[string]any) tea.Cmd {
	return func() tea.Msg {
		stdoutPipe, err := shared.ConnectToLogs(m.shared.Session.Context())
		if err != nil {
			fmt.Println(err)
			return nil
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

func logToStr(data map[string]any, match string) string {
	time := shared.AnyToStr(data, "time")
	service := shared.AnyToStr(data, "service")
	level := shared.AnyToStr(data, "level")
	msg := shared.AnyToStr(data, "msg")
	errMsg := shared.AnyToStr(data, "err")
	status := shared.AnyToInt(data, "status")
	url := shared.AnyToStr(data, "url")

	if match != "" {
		lvlMatch := matched(level, match)
		msgMatch := matched(msg, match)
		serviceMatch := matched(service, match)
		errMatch := matched(errMsg, match)
		urlMatch := matched(url, match)
		if !lvlMatch && !msgMatch && !serviceMatch && !errMatch && !urlMatch {
			return ""
		}
	}

	acc := fmt.Sprintf(
		"%s\t%s\t%s\t%s\t%s\t%d\t%s",
		time,
		service,
		level,
		msg,
		errMsg,
		status,
		url,
	)
	acc += "\n"
	return acc
}

func logsToStr(data []map[string]any, filter string) string {
	acc := ""
	for _, d := range data {
		res := logToStr(d, filter)
		if res != "" {
			acc += res
		}
	}
	return acc
}
