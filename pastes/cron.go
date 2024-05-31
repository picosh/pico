package pastes

import (
	"time"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
)

func deleteExpiredPosts(cfg *shared.ConfigSite, dbpool db.DB) error {
	cfg.Logger.Info("checking for expired posts")
	posts, err := dbpool.FindExpiredPosts(cfg.Space)
	if err != nil {
		return err
	}

	postIds := []string{}
	for _, post := range posts {
		postIds = append(postIds, post.ID)
	}

	cfg.Logger.Info("deleting expired posts", "len", len(postIds))
	err = dbpool.RemovePosts(postIds)
	if err != nil {
		return err
	}

	return nil
}

func CronDeleteExpiredPosts(cfg *shared.ConfigSite, dbpool db.DB) {
	for {
		err := deleteExpiredPosts(cfg, dbpool)
		if err != nil {
			cfg.Logger.Error(err.Error())
		}
		time.Sleep(12 * time.Hour)
	}
}
