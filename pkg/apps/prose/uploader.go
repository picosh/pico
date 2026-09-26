package prose

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
)

type ctxFeatureFlagKey struct{}

func getFeatureFlag(s *pssh.SSHServerConnSession) *db.FeatureFlag {
	v := s.Context().Value(ctxFeatureFlagKey{})
	if v == nil {
		return nil
	}
	ff := s.Context().Value(ctxFeatureFlagKey{}).(*db.FeatureFlag)
	return ff
}

func setFeatureFlag(s *pssh.SSHServerConnSession, ff *db.FeatureFlag) {
	s.SetValue(ctxFeatureFlagKey{}, ff)
}

func setFeatureLimits(ff *db.FeatureFlag, cfg *shared.ConfigSite) {
	ff.Data.StorageMax = ff.FindStorageMax(cfg.MaxSize)
	ff.Data.FileMax = ff.FindFileMax(cfg.MaxAssetSize)
	ff.Data.SpecialFileMax = ff.FindSpecialFileMax(cfg.MaxSpecialFileSize)
}

func findFeatureFlag(dbpool db.DB, cfg *shared.ConfigSite, userID string) (*db.FeatureFlag, error) {
	ff, err := dbpool.FindFeature(userID, "plus")
	if err == nil {
		if ff.IsValid() {
			setFeatureLimits(ff, cfg)
			return ff, nil
		}
		err = fmt.Errorf("ERROR: your pico+ has expired: https://blog.pico.sh/ann-038-pico-invite-system")
	}

	ffProse, proseErr := dbpool.FindFeature(userID, "prose")
	if proseErr == nil {
		if ffProse.IsValid() {
			setFeatureLimits(ffProse, cfg)
			return ffProse, nil
		}
		proseErr = fmt.Errorf("ERROR: your prose access has expired: https://blog.pico.sh/ann-038-pico-invite-system")
	}

	if err != nil && strings.Contains(err.Error(), "expired") {
		return nil, err
	}
	if proseErr != nil && strings.Contains(proseErr.Error(), "expired") {
		return nil, proseErr
	}
	return nil, fmt.Errorf("ERROR: uploading to prose requires an invitation or pico+: https://blog.pico.sh/ann-038-pico-invite-system")
}

type UploadHandler struct {
	*filehandlers.FileHandlerRouter
	Cfg *shared.ConfigSite
	DB  db.DB
}

var _ sendutils.CopyFromClientHandler = &UploadHandler{}
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

	ff, err := findFeatureFlag(h.DB, h.Cfg, user.ID)
	if err != nil {
		return err
	}
	setFeatureFlag(s, ff)

	return h.FileHandlerRouter.Validate(s)
}
