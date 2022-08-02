package lists

import (
	"fmt"
	"strings"

	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/shared"
	"golang.org/x/exp/slices"
)

type ListHooks struct {
	Cfg *shared.ConfigSite
}

func (p *ListHooks) FileValidate(text string, filename string) (bool, error) {
	if !shared.IsTextFile(text) {
		err := fmt.Errorf(
			"WARNING: (%s) invalid file must be plain text (utf-8), skipping",
			filename,
		)
		return false, err
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

func (p *ListHooks) FileMeta(text string, data *filehandlers.PostMetaData) error {
	parsedText := ParseText(text)

	if parsedText.MetaData.Title != "" {
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
