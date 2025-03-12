package main

import (
	"database/sql"
	"log/slog"
	"os"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/shared"
)

func findPosts(dbpool *sql.DB) ([]*db.Post, error) {
	var posts []*db.Post
	rs, err := dbpool.Query(`SELECT
		posts.id, user_id, filename, title, text, description,
		posts.created_at, publish_at, posts.updated_at, hidden, COALESCE(views, 0) as views
		FROM posts
		WHERE cur_space = 'prose'
	`)
	if err != nil {
		return posts, err
	}
	for rs.Next() {
		post := &db.Post{}
		err := rs.Scan(
			&post.ID,
			&post.UserID,
			&post.Filename,
			&post.Title,
			&post.Text,
			&post.Description,
			&post.CreatedAt,
			&post.PublishAt,
			&post.UpdatedAt,
			&post.Hidden,
			&post.Views,
		)
		if err != nil {
			return posts, err
		}

		posts = append(posts, post)
	}
	if rs.Err() != nil {
		return posts, rs.Err()
	}
	return posts, nil
}

func main() {
	logger := slog.Default()

	picoCfg := shared.NewConfigSite()
	picoCfg.Logger = logger
	picoCfg.DbURL = os.Getenv("DATABASE_URL")
	picoDb := postgres.NewDB(picoCfg.DbURL, picoCfg.Logger)

	logger.Info("fetching all posts")
	posts, err := findPosts(picoDb.Db)
	if err != nil {
		panic(err)
	}

	logger.Info("replacing tags")
	for _, post := range posts {
		parsed, err := shared.ParseText(post.Text)
		if err != nil {
			continue
		}
		if len(parsed.Tags) > 0 {
			err := picoDb.ReplaceTagsForPost(parsed.Tags, post.ID)
			panic(err)
		}
	}
}
