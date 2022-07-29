package pastes

import (
	"time"

	"git.sr.ht/~erock/pico/wish/cms/db"
)

func deleteExpiredPosts(cfg *ConfigSite, dbpool db.DB) error {
	cfg.Logger.Infof("checking for expired posts")
	now := time.Now()
	// delete posts that are older than three days
	expired := now.AddDate(0, 0, -3)
	posts, err := dbpool.FindPostsBeforeDate(&expired, cfg.Space)
	if err != nil {
		return err
	}

	postIds := []string{}
	for _, post := range posts {
		postIds = append(postIds, post.ID)
	}

	cfg.Logger.Infof("deleteing (%d) expired posts", len(postIds))
	err = dbpool.RemovePosts(postIds)
	if err != nil {
		return err
	}

	return nil
}

func CronDeleteExpiredPosts(cfg *ConfigSite, dbpool db.DB) {
	for {
		err := deleteExpiredPosts(cfg, dbpool)
		if err != nil {
			cfg.Logger.Error(err)
		}
		time.Sleep(1 * time.Hour)
	}
}
