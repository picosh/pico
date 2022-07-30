package pastes

import (
	"fmt"

	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/shared"
)

type PastesHandler struct {
	Cfg *shared.ConfigSite
}

func (p *PastesHandler) FileValidate(text string, filename string) (bool, error) {
	if !shared.IsTextFile(text, filename, p.Cfg.AllowedExt) {
		err := fmt.Errorf(
			"WARNING: (%s) invalid file, the contents must be plain text, skipping",
			filename,
		)
		return false, err
	}

	return true, nil
}

func (p *PastesHandler) FileMeta(text string, data *filehandlers.PostMetaData) error {
	return nil
}
