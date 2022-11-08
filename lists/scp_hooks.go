package lists

import (
	"fmt"
	"strings"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/imgs"
	"git.sr.ht/~erock/pico/shared"
	"golang.org/x/exp/slices"
)

type ListHooks struct {
	Cfg *shared.ConfigSite
	Db  db.DB
}

func (p *ListHooks) FileValidate(data *filehandlers.PostMetaData) (bool, error) {
	if !shared.IsTextFile(string(data.Text)) {
		err := fmt.Errorf(
			"WARNING: (%s) invalid file must be plain text (utf-8), skipping",
			data.Filename,
		)
		return false, err
	}

	if !shared.IsExtAllowed(data.Filename, p.Cfg.AllowedExt) {
		extStr := strings.Join(p.Cfg.AllowedExt, ",")
		err := fmt.Errorf(
			"WARNING: (%s) invalid file, format must be (%s), skipping",
			data.Filename,
			extStr,
		)
		return false, err
	}

	return true, nil
}

func (p *ListHooks) FileMeta(data *filehandlers.PostMetaData) error {
	linkify := imgs.NewImgsLinkify(data.Username)
	parsedText := ParseText(string(data.Text), linkify)

	if parsedText.MetaData.Title == "" {
		data.Title = shared.ToUpper(data.Slug)
	} else {
		data.Title = parsedText.MetaData.Title
	}

	data.Description = parsedText.MetaData.Description
	data.Tags = parsedText.MetaData.Tags

	if parsedText.MetaData.PublishAt != nil && !parsedText.MetaData.PublishAt.IsZero() {
		data.PublishAt = parsedText.MetaData.PublishAt
	}

	data.Hidden = slices.Contains(p.Cfg.HiddenPosts, data.Filename)

	return nil
}
