package prose

import (
	"fmt"
	"log/slog"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/storage"
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

type UploadHandler struct {
	*filehandlers.FileHandlerRouter
	Cfg *Config
	DB  DB
}

var _ sendutils.CopyFromClientHandler = &UploadHandler{}
var _ sendutils.CopyFromClientHandler = (*UploadHandler)(nil)

func NewUploadHandler(cfg *Config, dbpool DB, fileMap map[string]filehandlers.ReadWriteHandler) *UploadHandler {
	router := filehandlers.NewFileHandlerRouter(cfg.Space, fileMap)
	return &UploadHandler{
		FileHandlerRouter: router,
		Cfg:               cfg,
		DB:                dbpool,
	}
}

// NewFileHandler routes prose's text files to posts and the rest to images.
func NewFileHandler(cfg *Config, dbpool DB, st storage.StorageServe) *UploadHandler {
	// One post handler serves every text extension, so a post can be
	// renamed in place from one to another.
	posts := &postUploader{Cfg: cfg, DB: dbpool}
	return NewUploadHandler(cfg, dbpool, map[string]filehandlers.ReadWriteHandler{
		".md":      posts,
		".txt":     posts,
		".css":     posts,
		".lxt":     posts,
		"fallback": NewUploadImgHandler(dbpool, cfg, st),
	})
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

	ff, err := db.FindAccess(h.DB, user.ID, "prose")
	if err != nil {
		return err
	}
	ff.SetLimits(h.Cfg.MaxSize, h.Cfg.MaxAssetSize, h.Cfg.MaxSpecialFileSize)
	setFeatureFlag(s, ff)

	return h.FileHandlerRouter.Validate(s)
}
