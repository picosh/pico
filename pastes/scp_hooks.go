package pastes

import (
	"fmt"

	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/shared"
)

type FileHooks struct {
	Cfg *shared.ConfigSite
}

func (p *FileHooks) FileValidate(text string, filename string) (bool, error) {
	if !shared.IsTextFile(text) {
		err := fmt.Errorf(
			"WARNING: (%s) invalid file must be plain text (utf-8), skipping",
			filename,
		)
		return false, err
	}

	return true, nil
}

func (p *FileHooks) FileMeta(text string, data *filehandlers.PostMetaData) error {
	// we want the slug to be the filename for pastes
	data.Slug = data.Filename
	return nil
}
