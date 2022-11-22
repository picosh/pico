package main

import (
	"log"
	"os"

	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/cms/config"
	"go.uber.org/zap"
)

func createLogger() *zap.SugaredLogger {
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatal(err)
	}

	return logger.Sugar()
}

func main() {
	logger := createLogger()
	picoCfg := config.NewConfigCms()
	picoCfg.Logger = logger
	picoCfg.DbURL = os.Getenv("DATABASE_URL")
	picoDb := postgres.NewDB(picoCfg)

	logger.Info("fetching all posts")
	posts, err := picoDb.FindPosts()
	if err != nil {
		panic(err)
	}

	empty := 0
	diff := 0
	for _, post := range posts {
		nextShasum := shared.Shasum([]byte(post.Text))
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

	logger.Infof("empty (%d), diff (%d)", empty, diff)
}
