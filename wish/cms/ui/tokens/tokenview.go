package tokens

import (
	"fmt"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/wish/cms/ui/common"
)

type styledKey struct {
	styles       common.Styles
	nameLabel    string
	name         string
	date         string
	gutter       string
	dateLabel    string
	dateVal      string
	expiresLabel string
	expiresVal   string
}

func (m Model) newStyledKey(styles common.Styles, token *db.Token, active bool) styledKey {
	date := token.CreatedAt.Format("02 Jan 2006 15:04:05 MST")
	expires := token.ExpiresAt.Format("02 Jan 2006 15:04:05 MST")

	// Default state
	return styledKey{
		styles:       styles,
		date:         date,
		name:         token.Name,
		gutter:       " ",
		nameLabel:    "Name:",
		dateLabel:    "Added:",
		dateVal:      styles.LabelDim.Render(date),
		expiresLabel: "Expires:",
		expiresVal:   styles.LabelDim.Render(expires),
	}
}

// Selected state.
func (k *styledKey) selected() {
	k.gutter = common.VerticalLine(k.styles.Renderer, common.StateSelected)
	k.nameLabel = k.styles.Label.Render("Name:")
	k.dateLabel = k.styles.Label.Render("Added:")
	k.expiresLabel = k.styles.Label.Render("Expires:")
}

// Deleting state.
func (k *styledKey) deleting() {
	k.gutter = common.VerticalLine(k.styles.Renderer, common.StateDeleting)
	k.nameLabel = k.styles.Delete.Render("Name:")
	k.dateLabel = k.styles.Delete.Render("Added:")
	k.dateVal = k.styles.DeleteDim.Render(k.date)
	k.expiresLabel = k.styles.Delete.Render("Expires:")
	k.expiresVal = k.styles.DeleteDim.Render(k.expiresVal)
}

func (k styledKey) render(state keyState) string {
	switch state {
	case keySelected:
		k.selected()
	case keyDeleting:
		k.deleting()
	}
	return fmt.Sprintf(
		"%s %s %s\n%s %s %s\n%s %s %s\n",
		k.gutter, k.nameLabel, k.name,
		k.gutter, k.dateLabel, k.dateVal,
		k.gutter, k.expiresLabel, k.expiresVal,
	)
}
