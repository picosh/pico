package feeds

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/adhocore/gronx"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
)

type FeedHooks struct {
	Cfg *shared.ConfigSite
	Db  db.DB
}

func (p *FeedHooks) FileValidate(s *pssh.SSHServerConnSession, data *filehandlers.PostMetaData) (bool, error) {
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

	// Because we need to support sshfs, sftp runs our Write handler twice
	// and on the first pass we do not have access to the file data.
	// In that case we should skip the parsing validation
	if data.Text == "" {
		return true, nil
	}

	parsed := shared.ListParseText(string(data.Text))
	if parsed.Email == "" {
		return false, fmt.Errorf("ERROR: no email variable detected for %s, check the format of your file, skipping", data.Filename)
	}

	if parsed.DigestInterval != "" {
		return false, fmt.Errorf("ERROR: `digest_interval` is deprecated; use `cron`: https://pico.sh/feeds#cron")
	}

	if parsed.Cron != "" {
		if !gronx.IsValid(parsed.Cron) {
			return false, fmt.Errorf("ERROR: `cron` is invalid, reference: https://github.com/adhocore/gronx?tab=readme-ov-file#cron-expression")
		}
	}

	var allErr error
	for _, txt := range parsed.Items {
		u := ""
		if txt.IsText {
			u = txt.Value
		} else if txt.IsURL {
			u = string(txt.URL)
		}

		_, err := url.Parse(u)
		if err != nil {
			allErr = errors.Join(allErr, fmt.Errorf("%s: %w", u, err))
			continue
		}
	}
	if allErr != nil {
		return false, fmt.Errorf("ERROR: some urls provided were invalid check the format of your file, skipping: %w", allErr)
	}

	return true, nil
}

func (p *FeedHooks) FileMeta(s *pssh.SSHServerConnSession, data *filehandlers.PostMetaData) error {
	return nil
}
