package uploadimgs

import (
	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/imgs"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/send/send/utils"
)

type ImgsAPI struct {
	Cfg *shared.ConfigSite
	Db  db.DB
	St  storage.ObjectStorage
}

func NewImgsAPI(dbpool db.DB, st storage.ObjectStorage) *ImgsAPI {
	cfg := imgs.NewConfigSite()
	return &ImgsAPI{
		Cfg: cfg,
		Db:  dbpool,
		St:  st,
	}
}

func (img *ImgsAPI) HasAccess(userID string) bool {
	return img.Db.HasFeatureForUser(userID, "imgs")
}

func (img *ImgsAPI) Upload(s ssh.Session, file *utils.FileEntry) (string, error) {
	handler := NewUploadImgHandler(img.Db, img.Cfg, img.St)
	err := handler.Validate(s)
	if err != nil {
		return "", err
	}

	return handler.Write(s, file)
}
