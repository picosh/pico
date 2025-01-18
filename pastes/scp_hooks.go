package pastes

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/araddon/dateparse"
	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/filehandlers"
	fileshared "github.com/picosh/pico/filehandlers/shared"
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

var DEFAULT_EXPIRES_AT = 90

type FileHooks struct {
	Cfg *shared.ConfigSite
	Db  db.DB
}

func (p *FileHooks) FileValidate(s ssh.Session, data *filehandlers.PostMetaData) (bool, error) {
	if !utils.IsTextFile(string(data.Text)) {
		err := fmt.Errorf(
			"WARNING: (%s) invalid file must be plain text (utf-8), skipping",
			data.Filename,
		)
		return false, err
	}

	return true, nil
}

func (p *FileHooks) FileMeta(s ssh.Session, data *filehandlers.PostMetaData) error {
	data.Title = utils.ToUpper(data.Slug)
	// we want the slug to be the filename for pastes
	data.Slug = data.Filename

	if data.Post.ExpiresAt == nil || data.Post.ExpiresAt.IsZero() {
		// mark posts for deletion a X days after creation
		expiresAt := time.Now().AddDate(0, 0, DEFAULT_EXPIRES_AT)
		data.ExpiresAt = &expiresAt
	}

	var hidden bool
	var expiresFound bool
	var expires *time.Time

	cmd := s.Command()

	for _, arg := range cmd {
		if strings.Contains(arg, "=") {
			splitArg := strings.Split(arg, "=")
			if len(splitArg) != 2 {
				continue
			}

			switch splitArg[0] {
			case "hidden":
				val, err := strconv.ParseBool(splitArg[1])
				if err != nil {
					continue
				}

				hidden = val
			case "expires":
				val, err := strconv.ParseBool(splitArg[1])
				if err == nil {
					expiresFound = !val
					continue
				}

				duration, err := time.ParseDuration(splitArg[1])
				if err == nil {
					expiresFound = true
					expireTime := time.Now().Add(duration)
					expires = &expireTime
					continue
				}

				expireTime, err := dateparse.ParseStrict(splitArg[1])
				if err == nil {
					expiresFound = true
					expires = &expireTime
				}
			}
		}
	}

	data.Hidden = hidden

	if expiresFound {
		data.ExpiresAt = expires
	}

	return nil
}

func (p *FileHooks) FileSuccess(s ssh.Session, data *fileshared.FileUploaded) error {
	return nil
}
