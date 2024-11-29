package analytics

import (
	"context"
	"fmt"
	"strings"

	input "github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/pages"
	"github.com/picosh/utils"
)

type state int

const (
	stateLoading state = iota
	stateReady
)

type errMsg error

type SiteStatsLoaded struct {
	Summary *db.SummaryVisits
}

type SiteListLoaded struct {
	Sites []*db.VisitUrl
}

type PathStatsLoaded struct {
	Summary *db.SummaryVisits
}

type HasAnalyticsFeature struct {
	Has bool
}

type Model struct {
	shared           *common.SharedModel
	state            state
	logData          []map[string]any
	viewport         viewport.Model
	input            input.Model
	sub              chan map[string]any
	ctx              context.Context
	done             context.CancelFunc
	errMsg           error
	statsBySite      *db.SummaryVisits
	statsByPath      *db.SummaryVisits
	siteList         []*db.VisitUrl
	repl             string
	analyticsEnabled bool
}

func headerHeight(shrd *common.SharedModel) int {
	return shrd.HeaderHeight
}

func headerWidth(w int) int {
	return w - 2
}

var helpMsg = `This view shows site usage analytics for prose, pages, and tuns.

[Read our docs about site usage analytics](https://pico.sh/analytics)

Shortcuts:

- esc: leave page
- tab: toggle between viewport and input box
- ctrl+u: scroll viewport up a page
- ctrl+d: scroll viewport down a page
- j,k: scroll viewport

Commands: [help, stats, site {domain}, post {slug}]

**View usage stats for all sites for this month:**

> stats

**View usage stats for your site by month this year:**

> site pico.sh

**View usage stats for your site by day this month:**

> site pico.sh day

**View usage stats for your blog post by month this year:**

> post my-post

**View usage stats for blog posts by day this month:**

> post my-post day

`

func NewModel(shrd *common.SharedModel) Model {
	im := input.New()
	im.Cursor.Style = shrd.Styles.Cursor
	im.Placeholder = "type 'help' to learn how to use the repl"
	im.PlaceholderStyle = shrd.Styles.InputPlaceholder
	im.Prompt = shrd.Styles.FocusedPrompt.String()
	im.CharLimit = 50
	im.Focus()

	hh := headerHeight(shrd)
	ww := headerWidth(shrd.Width)
	inputHeight := lipgloss.Height(im.View())
	viewport := viewport.New(ww, shrd.Height-hh-inputHeight)
	viewport.YPosition = hh

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
	return m.hasAnalyticsFeature()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd
	updateViewport := true
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.viewport.Width = headerWidth(msg.Width)
		inputHeight := lipgloss.Height(m.input.View())
		hh := headerHeight(m.shared)
		m.viewport.Height = msg.Height - hh - inputHeight
		m.viewport.SetContent(m.renderViewport())

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
		// when typing in input, ignore viewport updates
		case " ", "k", "j":
			if m.input.Focused() {
				updateViewport = false
			}
		case "tab":
			if m.input.Focused() {
				m.input.Blur()
			} else {
				cmds = append(cmds, m.input.Focus())
			}
		case "enter":
			replCmd := m.input.Value()
			m.repl = replCmd
			if replCmd == "stats" {
				m.state = stateLoading
				cmds = append(cmds, m.fetchSiteList())
			} else if strings.HasPrefix(replCmd, "site") {
				name, by := splitReplCmd(replCmd)
				m.state = stateLoading
				cmds = append(cmds, m.fetchSiteStats(name, by))
			} else if strings.HasPrefix(replCmd, "post") {
				slug, by := splitReplCmd(replCmd)
				m.state = stateLoading
				cmds = append(cmds, m.fetchPostStats(slug, by))
			}

			m.viewport.SetContent(m.renderViewport())
			m.input.SetValue("")
		}

	case SiteStatsLoaded:
		m.state = stateReady
		m.statsBySite = msg.Summary
		m.viewport.SetContent(m.renderViewport())

	case PathStatsLoaded:
		m.state = stateReady
		m.statsByPath = msg.Summary
		m.viewport.SetContent(m.renderViewport())

	case SiteListLoaded:
		m.state = stateReady
		m.siteList = msg.Sites
		m.viewport.SetContent(m.renderViewport())

	case HasAnalyticsFeature:
		m.state = stateReady
		m.analyticsEnabled = msg.Has
		m.viewport.SetContent(m.renderViewport())
	}

	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	if updateViewport {
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if m.errMsg != nil {
		return m.shared.Styles.Error.Render(m.errMsg.Error())
	}
	return m.viewport.View() + "\n" + m.input.View()
}

func (m Model) renderViewport() string {
	if m.state == stateLoading {
		return "Loading ..."
	}

	if m.shared.PlusFeatureFlag == nil || !m.shared.PlusFeatureFlag.IsValid() {
		return m.renderMd(`**Analytics is only available for pico+ users.**

[Read our docs about site usage analytics](https://pico.sh/analytics)`)
	}

	if !m.analyticsEnabled {
		return m.renderMd(`**Analytics must be enabled in the Settings page.**

[Read our docs about site usage analytics](https://pico.sh/analytics)`)
	}

	cmd := m.repl
	if cmd == "help" {
		return m.renderMd(helpMsg)
	} else if cmd == "stats" {
		return m.renderSiteList()
	} else if strings.HasPrefix(cmd, "site") {
		return m.renderSiteStats(m.statsBySite)
	} else if strings.HasPrefix(cmd, "post") {
		return m.renderSiteStats(m.statsByPath)
	}

	return m.renderMd(helpMsg)
}

func (m Model) renderMd(md string) string {
	r, _ := glamour.NewTermRenderer(
		// detect background color and pick either the default dark or light theme
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(m.viewport.Width),
	)
	out, _ := r.Render(md)
	return out
}

func (m Model) renderSiteStats(summary *db.SummaryVisits) string {
	name, by := splitReplCmd(m.repl)
	str := m.shared.Styles.Label.SetString(fmt.Sprintf("%s by %s\n", name, by)).String()

	if !strings.HasPrefix(m.repl, "post") {
		str += "Top URLs\n"
		topUrlsTbl := common.VisitUrlsTbl(summary.TopUrls, m.shared.Styles.Renderer, m.viewport.Width)
		str += topUrlsTbl.String()
	}

	str += "\nTop Referers\n"
	topRefsTbl := common.VisitUrlsTbl(summary.TopReferers, m.shared.Styles.Renderer, m.viewport.Width)
	str += topRefsTbl.String()

	if by == "day" {
		str += "\nUnique Visitors by Day this Month\n"
	} else {
		str += "\nUnique Visitors by Month this Year\n"
	}
	uniqueTbl := common.UniqueVisitorsTbl(summary.Intervals, m.shared.Styles.Renderer, m.viewport.Width)
	str += uniqueTbl.String()
	return str
}

func (m Model) fetchSiteStats(site string, interval string) tea.Cmd {
	return func() tea.Msg {
		opts := &db.SummaryOpts{
			Host: site,

			UserID:   m.shared.User.ID,
			Interval: interval,
		}

		if interval == "day" {
			opts.Origin = utils.StartOfMonth()
		} else {
			opts.Origin = utils.StartOfYear()
		}

		summary, err := m.shared.Dbpool.VisitSummary(opts)
		if err != nil {
			return errMsg(err)
		}

		return SiteStatsLoaded{summary}
	}
}

func (m Model) fetchPostStats(raw string, interval string) tea.Cmd {
	return func() tea.Msg {
		slug := raw
		if !strings.HasPrefix(slug, "/") {
			slug = "/" + raw
		}

		opts := &db.SummaryOpts{
			Path: slug,

			UserID:   m.shared.User.ID,
			Interval: interval,
		}

		if interval == "day" {
			opts.Origin = utils.StartOfMonth()
		} else {
			opts.Origin = utils.StartOfYear()
		}

		summary, err := m.shared.Dbpool.VisitSummary(opts)
		if err != nil {
			return errMsg(err)
		}

		return PathStatsLoaded{summary}
	}
}

func (m Model) renderSiteList() string {
	tbl := common.VisitUrlsTbl(m.siteList, m.shared.Styles.Renderer, m.viewport.Width)
	str := "Sites: Unique Visitors this Month\n"
	str += tbl.String()
	return str
}

func (m Model) fetchSiteList() tea.Cmd {
	return func() tea.Msg {
		siteList, err := m.shared.Dbpool.FindVisitSiteList(&db.SummaryOpts{
			UserID: m.shared.User.ID,
			Origin: utils.StartOfMonth(),
		})
		if err != nil {
			return errMsg(err)
		}
		return SiteListLoaded{siteList}
	}
}

func (m Model) hasAnalyticsFeature() tea.Cmd {
	return func() tea.Msg {
		feature := m.shared.Dbpool.HasFeatureForUser(m.shared.User.ID, "analytics")
		return HasAnalyticsFeature{feature}
	}
}

func splitReplCmd(replCmd string) (string, string) {
	replRaw := strings.SplitN(replCmd, " ", 3)
	name := strings.TrimSpace(replRaw[1])
	by := "month"
	if len(replRaw) > 2 {
		by = replRaw[2]
	}
	return name, by
}
