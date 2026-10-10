package pastes

import (
	"fmt"
	"log/slog"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
)

func checkAccess(dbpool db.DB, userID string) error {
	ffPlus, plusErr := dbpool.FindFeature(userID, "plus")
	if plusErr == nil && ffPlus.IsValid() {
		return nil
	}

	ffPastes, pastesErr := dbpool.FindFeature(userID, "pastes")
	if pastesErr == nil {
		if ffPastes.IsValid() {
			return nil
		}
		return fmt.Errorf("ERROR: your pastes access has expired: https://blog.pico.sh/ann-038-pico-invite-system")
	}

	if plusErr == nil {
		return fmt.Errorf("ERROR: your pico+ has expired: https://blog.pico.sh/ann-038-pico-invite-system")
	}
	return fmt.Errorf("ERROR: uploading to pastes requires an invitation or pico+: https://blog.pico.sh/ann-038-pico-invite-system")
}

type UploadHandler struct {
	*filehandlers.FileHandlerRouter
	Cfg *shared.ConfigSite
	DB  db.DB
}

var _ sendutils.CopyFromClientHandler = (*UploadHandler)(nil)

func NewUploadHandler(cfg *shared.ConfigSite, dbpool db.DB, fileMap map[string]filehandlers.ReadWriteHandler) *UploadHandler {
	router := filehandlers.NewFileHandlerRouter(cfg, dbpool, fileMap)
	return &UploadHandler{
		FileHandlerRouter: router,
		Cfg:               cfg,
		DB:                dbpool,
	}
}

func (h *UploadHandler) GetLogger(s *pssh.SSHServerConnSession) *slog.Logger {
	logger := pssh.GetLogger(s)
	if logger == nil {
		if h.Cfg != nil && h.Cfg.Logger != nil {
			return h.Cfg.Logger
		}
		return slog.Default()
	}
	return logger
}

func (h *UploadHandler) Validate(s *pssh.SSHServerConnSession) error {
	logger := h.GetLogger(s)
	user := pssh.GetUser(s)

	if user == nil {
		err := fmt.Errorf("could not get user from ctx")
		logger.Error("error getting user from ctx", "err", err)
		return err
	}

	if err := checkAccess(h.DB, user.ID); err != nil {
		return err
	}

	return h.FileHandlerRouter.Validate(s)
}
