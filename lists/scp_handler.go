package lists

import (
	"fmt"
	"strings"

	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/lists/pkg"
	"git.sr.ht/~erock/pico/shared"
	"golang.org/x/exp/slices"
)

type ListsHandler struct {
	Cfg *shared.ConfigSite
}

func (p *ListsHandler) FileValidate(text string, filename string) (bool, error) {
	if !shared.IsTextFile(text, filename, p.Cfg.AllowedExt) {
		extStr := strings.Join(p.Cfg.AllowedExt, ",")
		err := fmt.Errorf(
			"WARNING: (%s) invalid file, format must be (%s) and the contents must be plain text, skipping",
			filename,
			extStr,
		)
		return false, err
	}

	return true, nil
}

func (p *ListsHandler) FileMeta(text string, data *filehandlers.PostMetaData) error {
	parsedText := pkg.ParseText(text)

	if parsedText.MetaData.Title != "" {
		data.Title = parsedText.MetaData.Title
	}

	data.Description = parsedText.MetaData.Description

	if parsedText.MetaData.PublishAt != nil && !parsedText.MetaData.PublishAt.IsZero() {
		data.PublishAt = parsedText.MetaData.PublishAt
	}

	data.Hidden = slices.Contains(p.Cfg.HiddenPosts, data.Filename)

	return nil
}
