package main

import (
	"encoding/binary"
	"log/slog"
	"os"

	"github.com/picosh/pico/pkg/db/postgres"
)

func bail(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	logger := slog.Default()

	picoDb := postgres.NewDB(os.Getenv("DATABASE_URL"), logger)

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
