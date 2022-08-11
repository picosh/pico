package pastes

import (
	"fmt"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/shared"
)

type FileHooks struct {
	Cfg *shared.ConfigSite
	Db  db.DB
}

func (p *FileHooks) FileValidate(data *filehandlers.PostMetaData) (bool, error) {
	if !shared.IsTextFile(string(data.Text)) {
		err := fmt.Errorf(
			"WARNING: (%s) invalid file must be plain text (utf-8), skipping",
			data.Filename,
		)
		return false, err
	}

	return true, nil
}

func (p *FileHooks) FileMeta(data *filehandlers.PostMetaData) error {
	data.Title = shared.ToUpper(data.Slug)
	// we want the slug to be the filename for pastes
	data.Slug = data.Filename
	return nil
}
