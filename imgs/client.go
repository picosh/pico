package imgs

import (
	"git.sr.ht/~erock/pico/db"
	uploadimgs "git.sr.ht/~erock/pico/filehandlers/imgs"
	"git.sr.ht/~erock/pico/imgs/storage"
	"git.sr.ht/~erock/pico/shared"
	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/gliderlabs/ssh"
)

type IImgsAPI interface {
	HasAccess(userID string) bool
	Upload(file *utils.FileEntry) (string, error)
}

type ImgsAPI struct {
	Cfg *shared.ConfigSite
	Db  db.DB
	St  storage.ObjectStorage
}

func NewImgsAPI(dbpool db.DB) *ImgsAPI {
	cfg := NewConfigSite()
	return &ImgsAPI{
		Cfg: cfg,
		Db:  dbpool,
	}
}

func (img *ImgsAPI) HasAccess(userID string) bool {
	return img.Db.HasFeatureForUser(userID, "imgs")
}

func (img *ImgsAPI) Upload(s ssh.Session, file *utils.FileEntry) (string, error) {
	handler := uploadimgs.NewUploadImgHandler(img.Db, img.Cfg, img.St)
	err := handler.Validate(s)
	if err != nil {
		return "", err
	}

	return handler.Write(s, file)
}
