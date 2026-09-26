package pgs

import (
	"fmt"
	"strings"

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/db"
)

func setFeatureLimits(ff *db.FeatureFlag, cfg *PgsConfig) {
	ff.Data.StorageMax = ff.FindStorageMax(cfg.MaxSize)
	ff.Data.FileMax = ff.FindFileMax(cfg.MaxAssetSize)
	ff.Data.SpecialFileMax = ff.FindSpecialFileMax(cfg.MaxSpecialFileSize)
}

func findFeatureFlag(dbpool pgsdb.PgsDB, cfg *PgsConfig, userID string) (*db.FeatureFlag, error) {
	ff, err := dbpool.FindFeature(userID, "plus")
	if err == nil {
		if ff.IsValid() {
			setFeatureLimits(ff, cfg)
			return ff, nil
		}
		err = fmt.Errorf("ERROR: your pico+ has expired: https://blog.pico.sh/ann-038-pico-invite-system")
	}

	ffPgs, pgsErr := dbpool.FindFeature(userID, "pgs")
	if pgsErr == nil {
		if ffPgs.IsValid() {
			setFeatureLimits(ffPgs, cfg)
			return ffPgs, nil
		}
		pgsErr = fmt.Errorf("ERROR: your pgs access has expired: https://blog.pico.sh/ann-038-pico-invite-system")
	}

	if err != nil && strings.Contains(err.Error(), "expired") {
		return nil, err
	}
	if pgsErr != nil && strings.Contains(pgsErr.Error(), "expired") {
		return nil, pgsErr
	}
	return nil, fmt.Errorf("ERROR: uploading to pgs requires an invitation or pico+: https://blog.pico.sh/ann-038-pico-invite-system")
}
