package prose

import (
	"fmt"
	"strings"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/filehandlers"
	"github.com/picosh/pico/imgs"
	"github.com/picosh/pico/shared"
	"golang.org/x/exp/slices"
)

type MarkdownHooks struct {
	Cfg *shared.ConfigSite
	Db  db.DB
}

func (p *MarkdownHooks) FileValidate(data *filehandlers.PostMetaData) (bool, error) {
	if !shared.IsTextFile(data.Text) {
		err := fmt.Errorf(
			"WARNING: (%s) invalid file must be plain text (utf-8), skipping",
			data.Filename,
		)
		return false, err
	}

	// special styles css file we want to permit but no other css file.
	// sometimes the directory is provided in the filename, so we want to
	// remove that before we perform this check.
	if strings.Replace(data.Filename, "/", "", 1) == "_styles.css" {
		return true, nil
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

func (p *MarkdownHooks) FileMeta(data *filehandlers.PostMetaData) error {
	linkify := imgs.NewImgsLinkify("")
	parsedText, err := shared.ParseText(data.Text, linkify)
	// we return nil here because we don't want the file upload to fail
	if err != nil {
		return nil
	}

	if parsedText.Title == "" {
		data.Title = shared.ToUpper(data.Slug)
	} else {
		data.Title = parsedText.Title
	}

	data.Tags = parsedText.Tags
	data.Description = parsedText.Description

	if parsedText.PublishAt != nil && !parsedText.PublishAt.IsZero() {
		data.PublishAt = parsedText.MetaData.PublishAt
	}

	isHiddenFilename := slices.Contains(p.Cfg.HiddenPosts, data.Filename)
	data.Hidden = parsedText.MetaData.Hidden || isHiddenFilename

	return nil
}
