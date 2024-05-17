package settings

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/tui/common"
	"github.com/picosh/pico/tui/pages"
)

type state int

const (
	stateLoading state = iota
	stateReady
)

type focus int

const (
	focusNone = iota
	focusAnalytics
)

type featuresLoadedMsg []*db.FeatureFlag

type Model struct {
	shared   common.SharedModel
	features []*db.FeatureFlag
	state    state
	focus    focus
}

func NewModel(shrd common.SharedModel) Model {
	return Model{
		shared: shrd,
		state:  stateLoading,
		focus:  focusNone,
	}
}

func (m Model) Init() tea.Cmd {
	return m.fetchFeatures()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m, pages.Navigate(pages.MenuPage)
		case "tab":
			if m.focus == focusNone {
				m.focus = focusAnalytics
			} else {
				m.focus = focusNone
			}
		case "enter":
			if m.focus == focusAnalytics {
				return m, m.toggleAnalytics()
			}
		}

	case featuresLoadedMsg:
		m.state = stateReady
		m.focus = focusNone
		m.features = msg
	}
	return m, nil
}

func (m Model) View() string {
	if m.state == stateLoading {
		return "Loading ..."
	}
	return m.featuresView() + "\n" + m.analyticsView()
}

func (m Model) findAnalyticsFeature() *db.FeatureFlag {
	for _, feature := range m.features {
		if feature.Name == "analytics" {
			return feature
		}
	}
	return nil
}

func (m Model) analyticsView() string {
	banner := `Get usage statistics on your blog, blog posts, and
pages sites. For example, see unique visitors, most popular URLs,
and top referers.

We do not collect usage statistic unless analytics is enabled.
Further, when analytics are disabled we do not purge usage statistics.

We will only store usage statistics for 1 year from when the event
was created.`

	str := ""
	hasPlus := m.shared.PlusFeatureFlag != nil
	if hasPlus {
		ff := m.findAnalyticsFeature()
		hasFocus := m.focus == focusAnalytics
		if ff == nil {
			str += banner + "\n\nEnable analytics " + common.OKButtonView(m.shared.Styles, hasFocus, false)
		} else {
			str += "Disable analytics " + common.OKButtonView(m.shared.Styles, hasFocus, false)
		}
	} else {
		str += banner + "\n\n" + m.shared.Styles.Error.SetString("Analytics is only available to pico+ users.").String()
	}

	return m.shared.Styles.RoundedBorder.SetString(str).String()
}

func (m Model) featuresView() string {
	headers := []string{
		"Name",
		"Quota (GB)",
		"Expires At",
	}

	data := [][]string{}
	for _, feature := range m.features {
		storeMax := shared.BytesToGB(int(feature.FindStorageMax(0)))
		row := []string{
			feature.Name,
			fmt.Sprintf("%.2f", storeMax),
			feature.ExpiresAt.Format("2006-01-02"),
		}
		data = append(data, row)
	}
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(m.shared.Styles.Renderer.NewStyle().BorderForeground(common.Indigo)).
		Width(m.shared.Width).
		Headers(headers...).
		Rows(data...)
	return "Features\n" + t.String()
}

func (m Model) fetchFeatures() tea.Cmd {
	return func() tea.Msg {
		features, err := m.shared.Dbpool.FindFeaturesForUser(m.shared.User.ID)
		if err != nil {
			return common.ErrMsg{Err: err}
		}
		return featuresLoadedMsg(features)
	}
}

func (m Model) toggleAnalytics() tea.Cmd {
	return func() tea.Msg {
		if m.findAnalyticsFeature() == nil {
			now := time.Now()
			expiresAt := now.AddDate(100, 0, 0)
			_, err := m.shared.Dbpool.InsertFeature(m.shared.User.ID, "analytics", expiresAt)
			if err != nil {
				return common.ErrMsg{Err: err}
			}
		} else {
			err := m.shared.Dbpool.RemoveFeature(m.shared.User.ID, "analytics")
			if err != nil {
				return common.ErrMsg{Err: err}
			}
		}

		cmd := m.fetchFeatures()
		return cmd()
	}
}
