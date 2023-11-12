package posts

import (
	"fmt"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/wish/cms/config"
	"github.com/picosh/pico/wish/cms/ui/common"
)

type styledKey struct {
	styles         common.Styles
	date           string
	gutter         string
	postLabel      string
	dateLabel      string
	dateVal        string
	title          string
	urlLabel       string
	url            string
	views          int
	viewsLabel     string
	model          Model
	expiresAtLabel string
	expiresAt      string
}

func (m Model) newStyledKey(styles common.Styles, post *db.Post, urls config.ConfigURL) styledKey {
	publishAt := post.PublishAt

	expiresAt := styles.LabelDim.Render("never")
	if post.ExpiresAt != nil {
		expiresAt = styles.LabelDim.Render(post.ExpiresAt.Format("02 Jan, 2006"))
	}

	// Default state
	return styledKey{
		styles:         styles,
		gutter:         " ",
		postLabel:      "post:",
		date:           publishAt.String(),
		dateLabel:      "publish_at:",
		dateVal:        styles.LabelDim.Render(publishAt.Format("02 Jan, 2006")),
		title:          post.Title,
		urlLabel:       "url:",
		url:            urls.PostURL(post.Username, post.Slug),
		viewsLabel:     "views:",
		views:          post.Views,
		model:          m,
		expiresAtLabel: "expires_at:",
		expiresAt:      expiresAt,
	}
}

// Selected state.
func (k *styledKey) selected() {
	k.gutter = common.VerticalLine(common.StateSelected)
	k.postLabel = k.styles.Label.Render("post:")
	k.dateLabel = k.styles.Label.Render("publish_at:")
	k.viewsLabel = k.styles.Label.Render("views:")
	k.urlLabel = k.styles.Label.Render("url:")
	k.expiresAtLabel = k.styles.Label.Render("expires_at:")
}

// Deleting state.
func (k *styledKey) deleting() {
	k.gutter = common.VerticalLine(common.StateDeleting)
	k.postLabel = k.styles.Delete.Render("post:")
	k.dateLabel = k.styles.Delete.Render("publish_at:")
	k.urlLabel = k.styles.Delete.Render("url:")
	k.viewsLabel = k.styles.Delete.Render("views:")
	k.title = k.styles.DeleteDim.Render(k.title)
	k.expiresAtLabel = k.styles.Delete.Render("expires_at:")
}

func (k styledKey) render(state postState) string {
	switch state {
	case postSelected:
		k.selected()
	case postDeleting:
		k.deleting()
	}

	mainBody := fmt.Sprintf(
		"%s %s %s\n%s %s %s\n%s %s %d\n%s %s %s\n",
		k.gutter, k.postLabel, k.title,
		k.gutter, k.dateLabel, k.dateVal,
		k.gutter, k.viewsLabel, k.views,
		k.gutter, k.urlLabel, k.url,
	)

	if k.model.cfg.Space == "pastes" {
		mainBody += fmt.Sprintf("%s %s %s\n", k.gutter, k.expiresAtLabel, k.expiresAt)
	}

	return mainBody + "\n"
}
