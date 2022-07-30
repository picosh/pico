package prose

import (
	"fmt"
	"strings"

	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/shared"
	"golang.org/x/exp/slices"
)

// var hiddenPosts = []string{"_readme.md", "_styles.css"}
// var allowedExtensions = []string{".md", ".css"}

type ProseHandler struct {
	Cfg *shared.ConfigSite
}

func (p *ProseHandler) FileValidate(text string, filename string) (bool, error) {
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

func (p *ProseHandler) FileMeta(text string, data *filehandlers.PostMetaData) error {
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
