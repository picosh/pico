package prose

import (
	"fmt"
	"strings"

	"slices"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/filehandlers"
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
	pipeUtil "github.com/picosh/utils/pipe"
)

type MarkdownHooks struct {
	Cfg  *shared.ConfigSite
	Db   db.DB
	Pipe *pipeUtil.ReconnectReadWriteCloser
}

func (p *MarkdownHooks) FileValidate(s ssh.Session, data *filehandlers.PostMetaData) (bool, error) {
	if !utils.IsTextFile(data.Text) {
		err := fmt.Errorf(
			"ERROR: (%s) invalid file must be plain text (utf-8), skipping",
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

	if !utils.IsExtAllowed(data.Filename, p.Cfg.AllowedExt) {
		extStr := strings.Join(p.Cfg.AllowedExt, ",")
		err := fmt.Errorf(
			"ERROR: (%s) invalid file, format must be (%s), skipping",
			data.Filename,
			extStr,
		)
		return false, err
	}

	if data.FileSize > MAX_FILE_SIZE {
		return false, fmt.Errorf(
			"ERROR: file (%s) has exceeded maximum file size (%d bytes)",
			data.Filename,
			MAX_FILE_SIZE,
		)
	}

	return true, nil
}

func (p *MarkdownHooks) FileMeta(s ssh.Session, data *filehandlers.PostMetaData) error {
	parsedText, err := shared.ParseText(data.Text)
	if err != nil {
		return fmt.Errorf("%s: %w", data.Filename, err)
	}

	if parsedText.Title == "" {
		data.Title = utils.ToUpper(data.Slug)
	} else {
		data.Title = parsedText.Title
	}

	data.Aliases = parsedText.Aliases
	data.Tags = parsedText.Tags
	data.Description = parsedText.Description

	if parsedText.PublishAt != nil && !parsedText.PublishAt.IsZero() {
		data.PublishAt = parsedText.MetaData.PublishAt
	}

	isHiddenFilename := slices.Contains(p.Cfg.HiddenPosts, data.Filename)
	data.Hidden = parsedText.MetaData.Hidden || isHiddenFilename

	return nil
}
