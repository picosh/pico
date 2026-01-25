package prose

import (
	"fmt"
	"path/filepath"
	"strings"

	"slices"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	pipeUtil "github.com/picosh/utils/pipe"
)

type MarkdownHooks struct {
	Cfg  *shared.ConfigSite
	Db   db.DB
	Pipe *pipeUtil.ReconnectReadWriteCloser
}

func (p *MarkdownHooks) FileValidate(s *pssh.SSHServerConnSession, data *filehandlers.PostMetaData) (bool, error) {
	if !shared.IsTextFile(data.Text) {
		err := fmt.Errorf(
			"ERROR: (%s) invalid file must be plain text (utf-8), skipping",
			data.Filename,
		)
		return false, err
	}

	fp := strings.Replace(data.Filename, "/", "", 1)
	// special styles css file we want to permit but no other css file.
	// sometimes the directory is provided in the filename, so we want to
	// remove that before we perform this check.
	if fp == "_styles.css" {
		return true, nil
	}
	// allow users to upload robots file
	if fp == "robots.txt" {
		return true, nil
	}

	if !shared.IsExtAllowed(data.Filename, p.Cfg.AllowedExt) {
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

func (p *MarkdownHooks) metaLxt(data *filehandlers.PostMetaData) error {
	parsedText := shared.ListParseText(data.Text)

	if parsedText.Title == "" {
		data.Title = shared.ToUpper(data.Slug)
	} else {
		data.Title = parsedText.Title
	}

	data.Aliases = parsedText.Aliases
	data.Tags = parsedText.Tags
	data.Description = parsedText.Description

	if parsedText.PublishAt != nil && !parsedText.PublishAt.IsZero() {
		data.PublishAt = parsedText.PublishAt
	}
	data.Hidden = parsedText.Hidden

	return nil
}

func (p *MarkdownHooks) metaMd(data *filehandlers.PostMetaData) error {
	parsedText, err := shared.ParseText(data.Text)
	if err != nil {
		return fmt.Errorf("%s: %w", data.Filename, err)
	}

	if parsedText.Title == "" {
		data.Title = shared.ToUpper(data.Slug)
	} else {
		data.Title = parsedText.Title
	}

	data.Aliases = parsedText.Aliases
	data.Tags = parsedText.Tags
	data.Description = parsedText.Description

	if parsedText.PublishAt != nil && !parsedText.PublishAt.IsZero() {
		data.PublishAt = parsedText.PublishAt
	}
	data.Hidden = parsedText.Hidden

	return nil
}

func (p *MarkdownHooks) FileMeta(s *pssh.SSHServerConnSession, data *filehandlers.PostMetaData) error {
	ext := filepath.Ext(data.Filename)
	switch ext {
	case ".lxt":
		err := p.metaLxt(data)
		if err != nil {
			return fmt.Errorf("%s: %w", data.Filename, err)
		}
	case ".md":
		err := p.metaMd(data)
		if err != nil {
			return fmt.Errorf("%s: %w", data.Filename, err)
		}
	}

	isHiddenFilename := slices.Contains(p.Cfg.HiddenPosts, data.Filename)
	data.Hidden = data.Hidden || isHiddenFilename

	return nil
}
