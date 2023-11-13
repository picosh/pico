package imgs

import (
	"github.com/picosh/pico/wish/send/utils"
)

type IImgsAPI interface {
	HasAccess(userID string) bool
	Upload(file *utils.FileEntry) (string, error)
}
