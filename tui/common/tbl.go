package common

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/picosh/pico/db"
)

func UniqueVisitorsTbl(intervals []*db.VisitInterval) *table.Table {
	headers := []string{"Date", "Unique Visitors"}
	data := [][]string{}
	for _, d := range intervals {
		data = append(data, []string{
			d.Interval.Format(time.RFC3339Nano),
			fmt.Sprintf("%d", d.Visitors),
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		Headers(headers...).
		Rows(data...)
	return t
}

func VisitUrlsTbl(urls []*db.VisitUrl) *table.Table {
	headers := []string{"URL", "Count"}
	data := [][]string{}
	for _, d := range urls {
		data = append(data, []string{
			d.Url,
			fmt.Sprintf("%d", d.Count),
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		Headers(headers...).
		Rows(data...)
	return t
}

func VisitUrlsWithProjectTbl(projects []*db.Project, urls []*db.VisitUrl) *table.Table {
	headers := []string{"Project", "URL", "Count"}
	data := [][]string{}
	for _, d := range urls {
		if d.ProjectID == "" {
			continue
		}
		projectName := ""
		for _, project := range projects {
			if project.ID == d.ProjectID {
				projectName = project.Name
			}
		}
		data = append(data, []string{
			projectName,
			d.Url,
			fmt.Sprintf("%d", d.Count),
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		Headers(headers...).
		Rows(data...)
	return t
}
