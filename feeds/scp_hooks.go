package feeds

import (
	"errors"
	"fmt"
	"net/url"

	"strings"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/filehandlers"
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

type FeedHooks struct {
	Cfg *shared.ConfigSite
	Db  db.DB
}

func (p *FeedHooks) FileValidate(s ssh.Session, data *filehandlers.PostMetaData) (bool, error) {
	if !utils.IsTextFile(string(data.Text)) {
		err := fmt.Errorf(
			"WARNING: (%s) invalid file must be plain text (utf-8), skipping",
			data.Filename,
		)
		return false, err
	}

	if !utils.IsExtAllowed(data.Filename, p.Cfg.AllowedExt) {
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

func (p *FeedHooks) FileMeta(s ssh.Session, data *filehandlers.PostMetaData) error {
	if data.Data.LastDigest == nil {
		now := time.Now()
		data.Data.LastDigest = &now
	}

	return nil
}
