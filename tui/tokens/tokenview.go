package tokens

import (
	"fmt"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
)

type styledKey struct {
	styles    common.Styles
	nameLabel string
	name      string
	date      string
	gutter    string
	dateLabel string
	dateVal   string
}

func newStyledKey(styles common.Styles, token *db.Token, active bool) styledKey {
	date := token.CreatedAt.Format(common.DateFormat)

	// Default state
	return styledKey{
		styles:    styles,
		date:      date,
		name:      token.Name,
		gutter:    " ",
		nameLabel: "Name:",
		dateLabel: "Added:",
		dateVal:   styles.Label.Render(date),
	}
}

// Selected state.
func (k *styledKey) selected() {
	k.gutter = common.VerticalLine(k.styles.Renderer, common.StateSelected)
	k.nameLabel = k.styles.Label.Render("Name:")
	k.dateLabel = k.styles.Label.Render("Added:")
}

// Deleting state.
func (k *styledKey) deleting() {
	k.gutter = common.VerticalLine(k.styles.Renderer, common.StateDeleting)
	k.nameLabel = k.styles.Delete.Render("Name:")
	k.dateLabel = k.styles.Delete.Render("Added:")
	k.dateVal = k.styles.Delete.Render(k.date)
}

func (k styledKey) render(state keyState) string {
	switch state {
	case keySelected:
		k.selected()
	case keyDeleting:
		k.deleting()
	}
	return fmt.Sprintf(
		"%s %s %s\n%s %s %s\n\n",
		k.gutter, k.nameLabel, k.name,
		k.gutter, k.dateLabel, k.dateVal,
	)
}
