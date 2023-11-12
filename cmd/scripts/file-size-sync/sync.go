package main

import (
	"encoding/binary"
	"log"
	"os"

	"github.com/picosh/pico/db/postgres"
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

func bail(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	logger := createLogger()

	picoCfg := config.NewConfigCms()
	picoCfg.Logger = logger
	picoCfg.DbURL = os.Getenv("DATABASE_URL")
	picoDb := postgres.NewDB(picoCfg.DbURL, picoCfg.Logger)

	posts, err := picoDb.FindPosts()
	bail(err)
	for _, post := range posts {
		post.FileSize = binary.Size([]byte(post.Text))
		_, err := picoDb.UpdatePost(post)
		bail(err)
	}
}
