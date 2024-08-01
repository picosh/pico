package prose

import (
	"bytes"
	"fmt"
	"strings"

	"slices"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/filehandlers"
	"github.com/picosh/pico/shared"
	"github.com/sergi/go-diff/diffmatchpatch"
)

type MarkdownHooks struct {
	Cfg *shared.ConfigSite
	Db  db.DB
}

func (p *MarkdownHooks) FileValidate(s ssh.Session, data *filehandlers.PostMetaData) (bool, error) {
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

func diffText(diffs []diffmatchpatch.Diff) string {
	var buff bytes.Buffer
	for _, diff := range diffs {
		text := diff.Text

		switch diff.Type {
		case diffmatchpatch.DiffInsert:
			_, _ = buff.WriteString("+")
			_, _ = buff.WriteString(text)
		case diffmatchpatch.DiffDelete:
			_, _ = buff.WriteString("-")
			_, _ = buff.WriteString(text)
		case diffmatchpatch.DiffEqual:
			_, _ = buff.WriteString(text)
		}
	}

	return buff.String()
}

func (p *MarkdownHooks) FileMeta(s ssh.Session, data *filehandlers.PostMetaData) error {
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

	if data.Cur.Text == data.Text {
	} else {
		dmp := diffmatchpatch.New()
		diffs := dmp.DiffMain(data.Cur.Text, data.Text, false)
		fmt.Println(diffs)
		data.Data.Diff = diffText(diffs)
	}

	if parsedText.PublishAt != nil && !parsedText.PublishAt.IsZero() {
		data.PublishAt = parsedText.MetaData.PublishAt
	}

	isHiddenFilename := slices.Contains(p.Cfg.HiddenPosts, data.Filename)
	data.Hidden = parsedText.MetaData.Hidden || isHiddenFilename

	return nil
}
