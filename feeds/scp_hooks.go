package feeds

import (
	"fmt"
	"strings"
	"time"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/filehandlers"
	"github.com/picosh/pico/imgs"
	"github.com/picosh/pico/shared"
	"golang.org/x/exp/slices"
)

type FeedHooks struct {
	Cfg *shared.ConfigSite
	Db  db.DB
}

func (p *FeedHooks) FileValidate(data *filehandlers.PostMetaData) (bool, error) {
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

func (p *FeedHooks) FileMeta(data *filehandlers.PostMetaData) error {
	linkify := imgs.NewImgsLinkify(data.Username)
	parsedText := shared.ListParseText(string(data.Text), linkify)

	if parsedText.Title == "" {
		data.Title = shared.ToUpper(data.Slug)
	} else {
		data.Title = parsedText.Title
	}

	data.Description = parsedText.Description
	data.Tags = parsedText.Tags

	data.Hidden = slices.Contains(p.Cfg.HiddenPosts, data.Filename)

	if data.Data.LastDigest == nil {
		now := time.Now()
		data.Data.LastDigest = &now
	}

	return nil
}
