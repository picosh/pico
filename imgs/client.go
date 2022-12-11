package imgs

import (
	"github.com/gliderlabs/ssh"
	"github.com/picosh/pico/db"
	uploadimgs "github.com/picosh/pico/filehandlers/imgs"
	"github.com/picosh/pico/imgs/storage"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/send/utils"
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

func NewImgsAPI(dbpool db.DB, st storage.ObjectStorage) *ImgsAPI {
	cfg := NewConfigSite()
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
	handler := uploadimgs.NewUploadImgHandler(img.Db, img.Cfg, img.St)
	err := handler.Validate(s)
	if err != nil {
		return "", err
	}

	return handler.Write(s, file)
}
