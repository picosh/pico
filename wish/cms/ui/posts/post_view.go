package posts

import (
	"fmt"

	"git.sr.ht/~erock/pico/wish/cms/config"
	"git.sr.ht/~erock/pico/wish/cms/db"
	"git.sr.ht/~erock/pico/wish/cms/ui/common"
)

type styledKey struct {
	styles    common.Styles
	date      string
	gutter    string
	postLabel string
	dateLabel string
	dateVal   string
	title     string
	urlLabel  string
	url       string
}

func (m Model) newStyledKey(styles common.Styles, post *db.Post, urls config.ConfigURL) styledKey {
	publishAt := post.PublishAt
	// Default state
	return styledKey{
		styles:    styles,
		gutter:    " ",
		postLabel: "post:",
		date:      publishAt.String(),
		dateLabel: "publish_at:",
		dateVal:   styles.LabelDim.Render(publishAt.Format("02 Jan, 2006")),
		title:     post.Title,
		urlLabel:  "url:",
		url:       urls.PostURL(post.Username, post.Slug),
	}
}

// Selected state.
func (k *styledKey) selected() {
	k.gutter = common.VerticalLine(common.StateSelected)
	k.postLabel = k.styles.Label.Render("post:")
	k.dateLabel = k.styles.Label.Render("publish_at:")
	k.urlLabel = k.styles.Label.Render("url:")
}

// Deleting state.
func (k *styledKey) deleting() {
	k.gutter = common.VerticalLine(common.StateDeleting)
	k.postLabel = k.styles.Delete.Render("post:")
	k.dateLabel = k.styles.Delete.Render("publish_at:")
	k.urlLabel = k.styles.Delete.Render("url:")
	k.title = k.styles.DeleteDim.Render(k.title)
}

func (k styledKey) render(state postState) string {
	switch state {
	case postSelected:
		k.selected()
	case postDeleting:
		k.deleting()
	}
	return fmt.Sprintf(
		"%s %s %s\n%s %s %s\n%s %s %s\n\n",
		k.gutter, k.postLabel, k.title,
		k.gutter, k.dateLabel, k.dateVal,
		k.gutter, k.urlLabel, k.url,
	)
}
