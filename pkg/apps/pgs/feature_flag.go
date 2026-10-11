package pgs

import (
	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/db"
)

// findFeatureFlag returns the flag that lets a user upload to pgs, with
// pgs's limits filled in.
func findFeatureFlag(dbpool pgsdb.PgsDB, cfg *Config, userID string) (*db.FeatureFlag, error) {
	ff, err := db.FindAccess(dbpool, userID, "pgs")
	if err != nil {
		return nil, err
	}
	ff.SetLimits(cfg.MaxSize, cfg.MaxAssetSize, cfg.MaxSpecialFileSize)
	return ff, nil
}
