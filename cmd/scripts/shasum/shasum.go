package main

import (
	"log/slog"
	"os"

	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

func main() {
	logger := slog.Default()
	picoCfg := shared.NewConfigSite()
	picoCfg.Logger = logger
	picoCfg.DbURL = os.Getenv("DATABASE_URL")
	picoDb := postgres.NewDB(picoCfg.DbURL, picoCfg.Logger)

	logger.Info("fetching all posts")
	posts, err := picoDb.FindPosts()
	if err != nil {
		panic(err)
	}

	empty := 0
	diff := 0
	for _, post := range posts {
		nextShasum := utils.Shasum([]byte(post.Text))
		if post.Shasum == "" {
			empty += 1
		} else if post.Shasum != nextShasum {
			diff += 1
		}
		post.Shasum = nextShasum

		_, err := picoDb.UpdatePost(post)
		if err != nil {
			panic(err)
		}
	}

	logger.Info("empty, diff", "empty", empty, "diff", diff)
}
