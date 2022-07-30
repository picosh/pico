package prose

import (
	"fmt"
	"strings"

	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/shared"
	"golang.org/x/exp/slices"
)

type MarkdownHooks struct {
	Cfg *shared.ConfigSite
}

func (p *MarkdownHooks) FileValidate(text string, filename string) (bool, error) {
	if !shared.IsTextFile(text) {
		err := fmt.Errorf(
			"WARNING: (%s) invalid file must be plain text (utf-8), skipping",
			filename,
		)
		return false, err
	}

	// special styles css file we want to permit but no other css file
	if filename == "_styles.css" {
		return true, nil
	}

	if !shared.IsExtAllowed(filename, p.Cfg.AllowedExt) {
		extStr := strings.Join(p.Cfg.AllowedExt, ",")
		err := fmt.Errorf(
			"WARNING: (%s) invalid file, format must be (%s), skipping",
			filename,
			extStr,
		)
		return false, err
	}

	return true, nil
}

func (p *MarkdownHooks) FileMeta(text string, data *filehandlers.PostMetaData) error {
	parsedText, err := ParseText(text)
	if err != nil {
		return err
	}

	if parsedText.Title != "" {
		data.Title = parsedText.Title
	}

	data.Description = parsedText.Description

	if parsedText.PublishAt != nil && !parsedText.PublishAt.IsZero() {
		data.PublishAt = parsedText.MetaData.PublishAt
	}

	data.Hidden = slices.Contains(p.Cfg.HiddenPosts, data.Filename)

	return nil
}
