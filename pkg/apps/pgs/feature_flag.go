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
		err = fmt.Errorf("your pico+ has expired")
	}

	ffPgs, pgsErr := dbpool.FindFeature(userID, "pgs")
	if pgsErr == nil {
		if ffPgs.IsValid() {
			setFeatureLimits(ffPgs, cfg)
			return ffPgs, nil
		}
		pgsErr = fmt.Errorf("your pgs access has expired")
	}

	if err != nil && strings.Contains(err.Error(), "expired") {
		return nil, err
	}
	if pgsErr != nil {
		return nil, pgsErr
	}
	return nil, err
}
