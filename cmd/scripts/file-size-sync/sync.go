package main

import (
	"encoding/binary"
	"log/slog"
	"os"

	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
)

func bail(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	logger := slog.Default()

	picoCfg := shared.NewConfigSite()
	picoCfg.Logger = logger
	picoCfg.DbURL = os.Getenv("DATABASE_URL")
	picoDb := postgres.NewDB(picoCfg.DbURL, picoCfg.Logger)

	posts, err := picoDb.FindPosts()
	bail(err)
	for _, post := range posts {
		if post.Space == "imgs" {
			continue
		}
		post.FileSize = binary.Size([]byte(post.Text))
		_, err := picoDb.UpdatePost(post)
		bail(err)
	}
}
