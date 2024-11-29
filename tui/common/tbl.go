package common

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/picosh/pico/db"
)

func UniqueVisitorsTbl(intervals []*db.VisitInterval, renderer *lipgloss.Renderer, maxWidth int) *table.Table {
	headers := []string{"Date", "Unique Visitors"}
	data := [][]string{}
	sum := 0
	for _, d := range intervals {
		data = append(data, []string{
			d.Interval.Format(time.DateOnly),
			fmt.Sprintf("%d", d.Visitors),
		})
		sum += d.Visitors
	}

	data = append(data, []string{
		"Total",
		fmt.Sprintf("%d", sum),
	})

	t := table.New().
		BorderStyle(renderer.NewStyle().BorderForeground(Indigo)).
		Width(maxWidth).
		Headers(headers...).
		Rows(data...)
	return t
}

func VisitUrlsTbl(urls []*db.VisitUrl, renderer *lipgloss.Renderer, maxWidth int) *table.Table {
	headers := []string{"URL", "Count"}
	data := [][]string{}
	sum := 0
	for _, d := range urls {
		data = append(data, []string{
			d.Url,
			fmt.Sprintf("%d", d.Count),
		})
		sum += d.Count
	}

	data = append(data, []string{
		"Total",
		fmt.Sprintf("%d", sum),
	})

	t := table.New().
		BorderStyle(renderer.NewStyle().BorderForeground(Indigo)).
		Width(maxWidth).
		Headers(headers...).
		Rows(data...)
	return t
}
